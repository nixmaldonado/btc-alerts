package notifier

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

const statusFired = "FIRED"

// Stream item attribute keys (see §4.1 of the design). Centralized so the decision
// helper and the formatter read the same names.
const (
	attrPK          = "PK"
	attrStatus      = "status"
	attrDirection   = "direction"
	attrTargetPrice = "targetPrice"
	attrFiredAt     = "firedAt"
)

const ownerPrefix = "OWNER#"

// shouldNotify reports whether a stream record represents a transition INTO the
// fired state. The table's stream FilterCriteria (PK begins_with OWNER#) lives in
// Terraform; this is the defensive in-handler check that makes delivery idempotent-
// friendly: only INSERT/MODIFY records whose NewImage is FIRED qualify, and a MODIFY
// only qualifies when the OldImage was not already FIRED (so unrelated updates and
// re-writes of an already-fired item never re-send).
func shouldNotify(rec events.DynamoDBEventRecord) bool {
	op := events.DynamoDBOperationType(rec.EventName)
	if op != events.DynamoDBOperationTypeInsert && op != events.DynamoDBOperationTypeModify {
		return false
	}
	if status, ok := rec.Change.NewImage[attrStatus]; !ok || status.String() != statusFired {
		return false
	}
	if op == events.DynamoDBOperationTypeModify {
		if old, ok := rec.Change.OldImage[attrStatus]; ok && old.String() == statusFired {
			return false
		}
	}
	return true
}

// attr safely reads a string-valued attribute, returning "" when absent.
func attr(img map[string]events.DynamoDBAttributeValue, key string) string {
	if v, ok := img[key]; ok {
		return v.String()
	}
	return ""
}

// num safely reads a number-valued attribute, returning "" when absent.
func num(img map[string]events.DynamoDBAttributeValue, key string) string {
	if v, ok := img[key]; ok {
		return v.Number()
	}
	return ""
}

// alertEmail formats the subject and body for a fired-alert email from a stream
// NewImage. The recipient is no longer on the item — it is resolved separately from
// the owner's profile. Missing attributes degrade to empty strings rather than
// panicking, so a malformed item still produces a sendable (if sparse) message.
func alertEmail(img map[string]events.DynamoDBAttributeValue) (subject, body string) {
	direction := attr(img, attrDirection)
	target := num(img, attrTargetPrice)
	firedAt := attr(img, attrFiredAt)

	subject = fmt.Sprintf("BTC Alert fired: %s %s", direction, target)
	body = fmt.Sprintf(
		"Your BTC price alert has fired.\n\n"+
			"Direction:    %s\n"+
			"Target price: %s USD\n"+
			"Fired at:     %s\n",
		direction, target, firedAt,
	)
	return subject, body
}

// ownerIDFromImage recovers the tenant id from the item's PK (OWNER#<id>).
func ownerIDFromImage(img map[string]events.DynamoDBAttributeValue) string {
	return strings.TrimPrefix(attr(img, attrPK), ownerPrefix)
}

// EmailSender is the narrow seam the Handler depends on: a single Send call.
// The real implementation wraps SES (see ses.go); unit tests use a recording fake.
type EmailSender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// EmailResolver looks up a tenant's notification email by owner id. *store.Store
// satisfies it directly. Resolving the recipient here — rather than reading it off
// the stream image — is what makes editing the profile email apply to every alert.
type EmailResolver interface {
	GetOwnerEmail(ctx context.Context, ownerID string) (email string, ok bool, err error)
}

// Handler processes a DynamoDB stream batch, emailing on transitions into FIRED.
type Handler struct {
	Sender   EmailSender
	Resolver EmailResolver
}

// Handle loops the stream records, skips any that do not represent a transition
// into FIRED, resolves the owner's email, formats the message, and sends it. A
// resolver error is returned so Lambda retries the batch (it may be transient); a
// tenant with no profile email is logged and skipped (retrying can't fix it). The
// first Send error is returned so the batch ultimately routes to the SQS DLQ.
func (h Handler) Handle(ctx context.Context, e events.DynamoDBEvent) error {
	for _, rec := range e.Records {
		if !shouldNotify(rec) {
			continue
		}
		ownerID := ownerIDFromImage(rec.Change.NewImage)
		to, ok, err := h.Resolver.GetOwnerEmail(ctx, ownerID)
		if err != nil {
			return fmt.Errorf("notifier: resolve owner %q email: %w", ownerID, err)
		}
		if !ok || to == "" {
			log.Printf("notifier: no profile email for owner %q, skipping", ownerID)
			continue
		}
		subject, body := alertEmail(rec.Change.NewImage)
		if err := h.Sender.Send(ctx, to, subject, body); err != nil {
			return fmt.Errorf("notifier: send email: %w", err)
		}
	}
	return nil
}
