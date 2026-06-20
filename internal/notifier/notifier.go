package notifier

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/events"
)

const statusFired = "FIRED"

// Stream item attribute keys (see §4.1 of the design). Centralized so the decision
// helper and the formatter read the same names.
const (
	attrStatus      = "status"
	attrEmail       = "email"
	attrDirection   = "direction"
	attrTargetPrice = "targetPrice"
	attrFiredAt     = "firedAt"
)

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

// alertEmail formats the recipient, subject, and body for a fired-alert email
// from a stream NewImage. Missing attributes degrade to empty strings rather than
// panicking, so a malformed item still produces a sendable (if sparse) message.
func alertEmail(img map[string]events.DynamoDBAttributeValue) (to, subject, body string) {
	to = attr(img, attrEmail)
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
	return to, subject, body
}

// EmailSender is the narrow seam the Handler depends on: a single Send call.
// The real implementation wraps SES (see ses.go); unit tests use a recording fake.
type EmailSender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Handler processes a DynamoDB stream batch, emailing on transitions into FIRED.
type Handler struct {
	Sender EmailSender
}

// Handle loops the stream records, skips any that do not represent a transition
// into FIRED, formats the email, and sends it. The first Send error is returned so
// Lambda retries the batch and ultimately routes it to the SQS dead-letter queue.
func (h Handler) Handle(ctx context.Context, e events.DynamoDBEvent) error {
	for _, rec := range e.Records {
		if !shouldNotify(rec) {
			continue
		}
		to, subject, body := alertEmail(rec.Change.NewImage)
		if err := h.Sender.Send(ctx, to, subject, body); err != nil {
			return fmt.Errorf("notifier: send email: %w", err)
		}
	}
	return nil
}
