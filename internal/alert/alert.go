package alert

import (
	"errors"
	"time"
)

type Status string

const (
	StatusArmed Status = "ARMED"
	StatusFired Status = "FIRED"
)

type Direction string

const (
	DirectionAbove Direction = "ABOVE"
	DirectionBelow Direction = "BELOW"
)

var (
	ErrNonPositivePrice      = errors.New("alert: prices must be positive")
	ErrTargetEqualsReference = errors.New("alert: target price equals reference price; direction is undefined")
)

// Alert is a price-target notification owned by an API key. The recipient email is
// not stored here: it lives once per tenant (the owner's profile item) so editing it
// updates every alert at once. The notifier resolves the owner's email at fire time.
type Alert struct {
	OwnerID        string
	ID             string
	Status         Status
	Direction      Direction
	TargetPrice    float64
	ReferencePrice float64
	Pct            *float64 // original percentage, if created as a % target
	CreatedAt      time.Time
	FiredAt        *time.Time
}

// Ref identifies a single alert by owner and id — the minimal key needed to fire
// it. The sparse GSI projects keys only, so a crossing query returns Refs rather
// than full Alerts: the rest of the item isn't in the index, and firing (a
// conditional update keyed by owner+id) never needs it.
type Ref struct {
	OwnerID string
	ID      string
}

// NewAlert builds an armed alert, deriving direction from target vs. reference price.
// id and now are injected so construction is deterministic and testable.
func NewAlert(ownerID, id string, targetPrice, referencePrice float64, pct *float64, now time.Time) (Alert, error) {
	if targetPrice <= 0 || referencePrice <= 0 {
		return Alert{}, ErrNonPositivePrice
	}
	if targetPrice == referencePrice {
		return Alert{}, ErrTargetEqualsReference
	}

	return Alert{
		OwnerID:        ownerID,
		ID:             id,
		Status:         StatusArmed,
		Direction:      directionFor(targetPrice, referencePrice),
		TargetPrice:    targetPrice,
		ReferencePrice: referencePrice,
		Pct:            pct,
		CreatedAt:      now,
	}, nil
}

// directionFor reports which way the price must move to reach the target: ABOVE when
// the target is above the reference, BELOW otherwise. Callers reject the equal case.
func directionFor(targetPrice, referencePrice float64) Direction {
	if targetPrice < referencePrice {
		return DirectionBelow
	}
	return DirectionAbove
}

// TargetFromPct resolves a percentage target to an absolute price.
func TargetFromPct(referencePrice, pct float64) float64 {
	return referencePrice * (1 + pct)
}

// Fire marks the alert fired at the given time.
func (a *Alert) Fire(now time.Time) {
	a.Status = StatusFired
	a.FiredAt = &now
}

// Rearm returns a fired alert to the armed state, re-deriving its direction from the
// supplied current price (as NewAlert does) so it watches its target from wherever the
// price is now — which may flip the original direction. It updates the reference price
// and clears the fired timestamp. Returns ErrNonPositivePrice for a non-positive price
// and ErrTargetEqualsReference when the price sits exactly on the target (direction
// undefined); on either error the alert is left unchanged.
func (a *Alert) Rearm(referencePrice float64) error {
	if referencePrice <= 0 {
		return ErrNonPositivePrice
	}
	if a.TargetPrice == referencePrice {
		return ErrTargetEqualsReference
	}
	a.Direction = directionFor(a.TargetPrice, referencePrice)
	a.ReferencePrice = referencePrice
	a.Status = StatusArmed
	a.FiredAt = nil
	return nil
}
