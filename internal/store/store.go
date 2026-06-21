package store

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
)

// GSIName is the sparse status/target global secondary index (design §4.3).
const GSIName = "status-target-index"

var (
	// ErrNotFound is returned when an alert does not exist.
	ErrNotFound = errors.New("store: alert not found")
	// ErrNotArmed is returned when a conditional fire targets a missing or already-fired alert.
	ErrNotArmed = errors.New("store: alert not armed")
)

// Store persists alerts and the last-price singleton in one DynamoDB table.
type Store struct {
	client *dynamodb.Client
	table  string
}

// New builds a Store over the given client and table name.
func New(client *dynamodb.Client, table string) *Store {
	return &Store{client: client, table: table}
}

// PutAlert writes (creates or replaces) an alert item.
func (s *Store) PutAlert(ctx context.Context, a alert.Alert) error {
	_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      toItem(a),
	})
	return err
}

// GetAlert fetches one alert by owner + id, returning ErrNotFound when absent.
func (s *Store) GetAlert(ctx context.Context, ownerID, id string) (alert.Alert, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			attrPK: s.pk(ownerID),
			attrSK: s.sk(id),
		},
	})
	if err != nil {
		return alert.Alert{}, err
	}
	if out.Item == nil {
		return alert.Alert{}, ErrNotFound
	}
	return fromItem(out.Item)
}

// ListAlerts returns every alert owned by ownerID (PK = OWNER#owner, SK begins_with ALERT#),
// following the Query paginator across pages.
func (s *Store) ListAlerts(ctx context.Context, ownerID string) ([]alert.Alert, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		KeyConditionExpression: aws.String("#pk = :pk AND begins_with(#sk, :skprefix)"),
		ExpressionAttributeNames: map[string]string{
			"#pk": attrPK,
			"#sk": attrSK,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":       s.pk(ownerID),
			":skprefix": &types.AttributeValueMemberS{Value: "ALERT#"},
		},
	}

	var alerts []alert.Alert
	paginator := dynamodb.NewQueryPaginator(s.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			a, err := fromItem(it)
			if err != nil {
				return nil, err
			}
			alerts = append(alerts, a)
		}
	}
	return alerts, nil
}

// DeleteAlert removes one alert, returning ErrNotFound if it did not exist
// (the attribute_exists condition fails when the item is absent).
func (s *Store) DeleteAlert(ctx context.Context, ownerID, id string) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			attrPK: s.pk(ownerID),
			attrSK: s.sk(id),
		},
		ConditionExpression:      aws.String("attribute_exists(#pk)"),
		ExpressionAttributeNames: map[string]string{"#pk": attrPK},
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// RearmAlert reads the alert, applies the domain Rearm transition, and writes it back.
// Routing through PutAlert restores the sparse gsi_pk/gsi_sk attributes so the alert
// re-enters the GSI. Returns the rearmed alert; ErrNotFound when absent.
func (s *Store) RearmAlert(ctx context.Context, ownerID, id string) (alert.Alert, error) {
	a, err := s.GetAlert(ctx, ownerID, id)
	if err != nil {
		return alert.Alert{}, err
	}
	a.Rearm()
	if err := s.PutAlert(ctx, a); err != nil {
		return alert.Alert{}, err
	}
	return a, nil
}

// pk / sk build the primary-key attribute values for an alert item.
func (s *Store) pk(ownerID string) types.AttributeValue {
	return &types.AttributeValueMemberS{Value: alert.OwnerPK(ownerID)}
}
func (s *Store) sk(id string) types.AttributeValue {
	return &types.AttributeValueMemberS{Value: alert.AlertSK(id)}
}

// GetLastPrice reads the STATE#PRICE singleton. ok is false when it has never been written.
func (s *Store) GetLastPrice(ctx context.Context) (price float64, ok bool, err error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			attrPK: &types.AttributeValueMemberS{Value: alert.StatePK()},
			attrSK: &types.AttributeValueMemberS{Value: alert.StateSK()},
		},
	})
	if err != nil {
		return 0, false, err
	}
	if out.Item == nil {
		return 0, false, nil
	}
	price, err = getN(out.Item, attrLastPrice)
	if err != nil {
		return 0, false, err
	}
	return price, true, nil
}

// PutLastPrice writes the STATE#PRICE singleton with the observed price and timestamp.
func (s *Store) PutLastPrice(ctx context.Context, price float64, now time.Time) error {
	_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item: map[string]types.AttributeValue{
			attrPK:         &types.AttributeValueMemberS{Value: alert.StatePK()},
			attrSK:         &types.AttributeValueMemberS{Value: alert.StateSK()},
			attrLastPrice:  n(price),
			attrLastSeenAt: s_(now),
		},
	})
	return err
}

// s_ formats a time as an RFC3339 string attribute (named to avoid clashing with the s helper).
func s_(t time.Time) types.AttributeValue {
	return &types.AttributeValueMemberS{Value: t.Format(time.RFC3339)}
}

// GetOwnerEmail reads the tenant's profile email (PK=OWNER#owner, SK=PROFILE).
// ok is false when the tenant has not set one yet.
func (s *Store) GetOwnerEmail(ctx context.Context, ownerID string) (email string, ok bool, err error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			attrPK: s.pk(ownerID),
			attrSK: &types.AttributeValueMemberS{Value: alert.ProfileSK()},
		},
	})
	if err != nil {
		return "", false, err
	}
	if out.Item == nil {
		return "", false, nil
	}
	email, err = getS(out.Item, attrEmail)
	if err != nil {
		return "", false, err
	}
	return email, true, nil
}

// PutOwnerEmail writes (creates or replaces) the tenant's profile email. Because the
// notifier resolves the recipient from this item at fire time, updating it changes
// delivery for all of a tenant's alerts, including ones already created.
func (s *Store) PutOwnerEmail(ctx context.Context, ownerID, email string) error {
	_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item: map[string]types.AttributeValue{
			attrPK:    s.pk(ownerID),
			attrSK:    &types.AttributeValueMemberS{Value: alert.ProfileSK()},
			attrEmail: &types.AttributeValueMemberS{Value: email},
		},
	})
	return err
}

// QueryArmedCrossed returns armed alerts of the given direction whose target lies in
// [low, high] inclusive, by ranging the sparse GSI. Only armed alerts carry gsi_pk/gsi_sk,
// so fired/disarmed alerts never appear.
func (s *Store) QueryArmedCrossed(ctx context.Context, dir alert.Direction, low, high float64) ([]alert.Ref, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		IndexName:              aws.String(GSIName),
		KeyConditionExpression: aws.String("#gpk = :gpk AND #gsk BETWEEN :lo AND :hi"),
		ExpressionAttributeNames: map[string]string{
			"#gpk": attrGSIPK,
			"#gsk": attrGSISK,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":gpk": &types.AttributeValueMemberS{Value: alert.GSIPK(dir)},
			":lo":  &types.AttributeValueMemberS{Value: alert.GSISK(low)},
			":hi":  &types.AttributeValueMemberS{Value: alert.GSISK(high)},
		},
	}

	// nil (not []alert.Ref{}) so an empty result deep-equals the no-hit case in
	// callers and tests; this value is never JSON-encoded.
	var refs []alert.Ref
	paginator := dynamodb.NewQueryPaginator(s.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			ref, err := refFromItem(it)
			if err != nil {
				return nil, err
			}
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// FireAlert conditionally transitions an alert ARMED→FIRED. The status=ARMED condition
// makes firing idempotent (a retry can't double-fire), and REMOVE on gsi_pk/gsi_sk drops
// the item from the sparse index. Returns ErrNotArmed if the alert is missing or not armed.
func (s *Store) FireAlert(ctx context.Context, ownerID, id string, now time.Time) error {
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			attrPK: s.pk(ownerID),
			attrSK: s.sk(id),
		},
		ConditionExpression: aws.String("#status = :armed"),
		UpdateExpression:    aws.String("SET #status = :fired, #firedAt = :now REMOVE #gpk, #gsk"),
		ExpressionAttributeNames: map[string]string{
			"#status":  attrStatus,
			"#firedAt": attrFiredAt,
			"#gpk":     attrGSIPK,
			"#gsk":     attrGSISK,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":armed": &types.AttributeValueMemberS{Value: string(alert.StatusArmed)},
			":fired": &types.AttributeValueMemberS{Value: string(alert.StatusFired)},
			":now":   s_(now),
		},
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrNotArmed
		}
		return err
	}
	return nil
}
