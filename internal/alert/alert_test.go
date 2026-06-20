package alert

import (
	"errors"
	"testing"
	"time"
)

var testNow = time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

func TestNewAlert_AboveWhenTargetExceedsReference(t *testing.T) {
	a, err := NewAlert("key123", "id1", "user@example.com", 71000, 70000, nil, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Direction != DirectionAbove {
		t.Errorf("direction = %q, want ABOVE", a.Direction)
	}
	if a.Status != StatusArmed {
		t.Errorf("status = %q, want ARMED", a.Status)
	}
	if a.CreatedAt != testNow {
		t.Errorf("createdAt = %v, want %v", a.CreatedAt, testNow)
	}
	if a.FiredAt != nil {
		t.Errorf("firedAt = %v, want nil", a.FiredAt)
	}
}

func TestNewAlert_BelowWhenTargetUnderReference(t *testing.T) {
	a, err := NewAlert("key123", "id2", "user@example.com", 65000, 70000, nil, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Direction != DirectionBelow {
		t.Errorf("direction = %q, want BELOW", a.Direction)
	}
}

func TestNewAlert_RejectsEqualTargetAndReference(t *testing.T) {
	_, err := NewAlert("key123", "id3", "user@example.com", 70000, 70000, nil, testNow)
	if !errors.Is(err, ErrTargetEqualsReference) {
		t.Errorf("err = %v, want ErrTargetEqualsReference", err)
	}
}

func TestNewAlert_RejectsNonPositivePrices(t *testing.T) {
	if _, err := NewAlert("k", "id", "user@example.com", 0, 70000, nil, testNow); !errors.Is(err, ErrNonPositivePrice) {
		t.Errorf("target=0: err = %v, want ErrNonPositivePrice", err)
	}
	if _, err := NewAlert("k", "id", "user@example.com", 71000, -1, nil, testNow); !errors.Is(err, ErrNonPositivePrice) {
		t.Errorf("reference<0: err = %v, want ErrNonPositivePrice", err)
	}
}

func TestNewAlert_RejectsEmptyEmail(t *testing.T) {
	if _, err := NewAlert("k", "id", "", 71000, 70000, nil, testNow); !errors.Is(err, ErrEmptyEmail) {
		t.Errorf("err = %v, want ErrEmptyEmail", err)
	}
}

func TestTargetFromPct(t *testing.T) {
	got := TargetFromPct(70000, 0.10)
	if got != 77000 {
		t.Errorf("TargetFromPct(70000, 0.10) = %v, want 77000", got)
	}
}
