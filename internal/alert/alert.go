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
	ErrEmptyEmail            = errors.New("alert: email must not be empty")
)

// Alert is a price-target notification owned by an API key.
type Alert struct {
	OwnerID        string
	ID             string
	Status         Status
	Direction      Direction
	TargetPrice    float64
	Email          string
	ReferencePrice float64
	Pct            *float64 // original percentage, if created as a % target
	CreatedAt      time.Time
	FiredAt        *time.Time
}

// NewAlert builds an armed alert, deriving direction from target vs. reference price.
// id and now are injected so construction is deterministic and testable.
func NewAlert(ownerID, id, email string, targetPrice, referencePrice float64, pct *float64, now time.Time) (Alert, error) {
	if targetPrice <= 0 || referencePrice <= 0 {
		return Alert{}, ErrNonPositivePrice
	}
	if email == "" {
		return Alert{}, ErrEmptyEmail
	}
	if targetPrice == referencePrice {
		return Alert{}, ErrTargetEqualsReference
	}

	dir := DirectionAbove
	if targetPrice < referencePrice {
		dir = DirectionBelow
	}

	return Alert{
		OwnerID:        ownerID,
		ID:             id,
		Status:         StatusArmed,
		Direction:      dir,
		TargetPrice:    targetPrice,
		Email:          email,
		ReferencePrice: referencePrice,
		Pct:            pct,
		CreatedAt:      now,
	}, nil
}

// TargetFromPct resolves a percentage target to an absolute price.
func TargetFromPct(referencePrice, pct float64) float64 {
	return referencePrice * (1 + pct)
}
