package store

import (
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
)

// Persisted attribute names (design §4). This file is the only place that knows them.
const (
	attrPK             = "PK"
	attrSK             = "SK"
	attrStatus         = "status"
	attrDirection      = "direction"
	attrTargetPrice    = "targetPrice"
	attrEmail          = "email"
	attrReferencePrice = "referencePrice"
	attrPct            = "pct"
	attrCreatedAt      = "createdAt"
	attrFiredAt        = "firedAt"
	attrGSIPK          = "gsi_pk"
	attrGSISK          = "gsi_sk"

	attrLastPrice  = "lastPrice"
	attrLastSeenAt = "lastSeenAt"
)

// s builds a DynamoDB string attribute.
func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

// n builds a DynamoDB number attribute from a float64, using the shortest exact representation.
func n(v float64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatFloat(v, 'f', -1, 64)}
}

// getS reads a required string attribute.
func getS(it map[string]types.AttributeValue, k string) (string, error) {
	av, ok := it[k]
	if !ok {
		return "", fmt.Errorf("store: attribute %q missing", k)
	}
	sv, ok := av.(*types.AttributeValueMemberS)
	if !ok {
		return "", fmt.Errorf("store: attribute %q is %T, want S", k, av)
	}
	return sv.Value, nil
}

// getN reads a required number attribute as float64.
func getN(it map[string]types.AttributeValue, k string) (float64, error) {
	av, ok := it[k]
	if !ok {
		return 0, fmt.Errorf("store: attribute %q missing", k)
	}
	nv, ok := av.(*types.AttributeValueMemberN)
	if !ok {
		return 0, fmt.Errorf("store: attribute %q is %T, want N", k, av)
	}
	f, err := strconv.ParseFloat(nv.Value, 64)
	if err != nil {
		return 0, fmt.Errorf("store: attribute %q not a float: %w", k, err)
	}
	return f, nil
}

// toItem marshals an Alert to its DynamoDB item map. gsi_pk/gsi_sk are written only
// while the alert is ARMED, so firing or disarming drops it from the sparse index.
func toItem(a alert.Alert) map[string]types.AttributeValue {
	it := map[string]types.AttributeValue{
		attrPK:             s(alert.OwnerPK(a.OwnerID)),
		attrSK:             s(alert.AlertSK(a.ID)),
		attrStatus:         s(string(a.Status)),
		attrDirection:      s(string(a.Direction)),
		attrTargetPrice:    n(a.TargetPrice),
		attrReferencePrice: n(a.ReferencePrice),
		attrCreatedAt:      s(a.CreatedAt.Format(time.RFC3339)),
	}
	if a.Pct != nil {
		it[attrPct] = n(*a.Pct)
	}
	if a.FiredAt != nil {
		it[attrFiredAt] = s(a.FiredAt.Format(time.RFC3339))
	}
	if a.Status == alert.StatusArmed {
		it[attrGSIPK] = s(alert.GSIPK(a.Direction))
		it[attrGSISK] = s(alert.GSISK(a.TargetPrice))
	}
	return it
}

// refFromItem extracts an alert's owner+id from a GSI query item. The sparse index
// projects keys only, so PK/SK are the only attributes present — and all the
// evaluator needs to fire the alert. It inverts the OWNER#/ALERT# prefixes the
// same way fromItem does.
func refFromItem(it map[string]types.AttributeValue) (alert.Ref, error) {
	pk, err := getS(it, attrPK)
	if err != nil {
		return alert.Ref{}, err
	}
	sk, err := getS(it, attrSK)
	if err != nil {
		return alert.Ref{}, err
	}
	return alert.Ref{
		OwnerID: pk[len("OWNER#"):],
		ID:      sk[len("ALERT#"):],
	}, nil
}

// fromItem reconstructs an Alert from a DynamoDB item map.
func fromItem(it map[string]types.AttributeValue) (alert.Alert, error) {
	pk, err := getS(it, attrPK)
	if err != nil {
		return alert.Alert{}, err
	}
	sk, err := getS(it, attrSK)
	if err != nil {
		return alert.Alert{}, err
	}
	status, err := getS(it, attrStatus)
	if err != nil {
		return alert.Alert{}, err
	}
	direction, err := getS(it, attrDirection)
	if err != nil {
		return alert.Alert{}, err
	}
	target, err := getN(it, attrTargetPrice)
	if err != nil {
		return alert.Alert{}, err
	}
	reference, err := getN(it, attrReferencePrice)
	if err != nil {
		return alert.Alert{}, err
	}
	createdAtStr, err := getS(it, attrCreatedAt)
	if err != nil {
		return alert.Alert{}, err
	}
	createdAt, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return alert.Alert{}, fmt.Errorf("store: createdAt %q not RFC3339: %w", createdAtStr, err)
	}

	a := alert.Alert{
		OwnerID:        pk[len("OWNER#"):],
		ID:             sk[len("ALERT#"):],
		Status:         alert.Status(status),
		Direction:      alert.Direction(direction),
		TargetPrice:    target,
		ReferencePrice: reference,
		CreatedAt:      createdAt,
	}

	if _, ok := it[attrPct]; ok {
		pct, err := getN(it, attrPct)
		if err != nil {
			return alert.Alert{}, err
		}
		a.Pct = &pct
	}
	if _, ok := it[attrFiredAt]; ok {
		firedAtStr, err := getS(it, attrFiredAt)
		if err != nil {
			return alert.Alert{}, err
		}
		firedAt, err := time.Parse(time.RFC3339, firedAtStr)
		if err != nil {
			return alert.Alert{}, fmt.Errorf("store: firedAt %q not RFC3339: %w", firedAtStr, err)
		}
		a.FiredAt = &firedAt
	}

	return a, nil
}
