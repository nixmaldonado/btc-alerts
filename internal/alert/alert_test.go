package alert

import (
	"errors"
	"testing"
	"time"
)

var testNow = time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

func TestNewAlert(t *testing.T) {
	tests := []struct {
		name          string
		ownerID       string
		id            string
		target        float64
		reference     float64
		pct           *float64
		wantErr       error
		wantDirection Direction
	}{
		{
			name:          "direction is ABOVE when target exceeds reference",
			ownerID:       "key123",
			id:            "id1",
			target:        71000,
			reference:     70000,
			wantDirection: DirectionAbove,
		},
		{
			name:          "direction is BELOW when target is under reference",
			ownerID:       "key123",
			id:            "id2",
			target:        65000,
			reference:     70000,
			wantDirection: DirectionBelow,
		},
		{
			name:      "rejects target equal to reference (direction undefined)",
			ownerID:   "key123",
			id:        "id3",
			target:    70000,
			reference: 70000,
			wantErr:   ErrTargetEqualsReference,
		},
		{
			name:      "rejects zero target price",
			id:        "id",
			target:    0,
			reference: 70000,
			wantErr:   ErrNonPositivePrice,
		},
		{
			name:      "rejects negative reference price",
			id:        "id",
			target:    71000,
			reference: -1,
			wantErr:   ErrNonPositivePrice,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAlert(tc.ownerID, tc.id, tc.target, tc.reference, tc.pct, testNow)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.Direction != tc.wantDirection {
				t.Errorf("direction = %q, want %q", a.Direction, tc.wantDirection)
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
		})
	}
}

func TestTargetFromPct(t *testing.T) {
	tests := []struct {
		name      string
		reference float64
		pct       float64
		want      float64
	}{
		{name: "ten percent above reference", reference: 70000, pct: 0.10, want: 77000},
		{name: "ten percent below reference", reference: 70000, pct: -0.10, want: 63000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TargetFromPct(tc.reference, tc.pct); got != tc.want {
				t.Errorf("TargetFromPct(%v, %v) = %v, want %v", tc.reference, tc.pct, got, tc.want)
			}
		})
	}
}

func TestAlertTransitions(t *testing.T) {
	fireTime := testNow.Add(time.Hour)

	tests := []struct {
		name       string
		apply      func(a *Alert)
		wantStatus Status
		wantFired  *time.Time
	}{
		{
			name:       "Fire sets FIRED status and the fired timestamp",
			apply:      func(a *Alert) { a.Fire(fireTime) },
			wantStatus: StatusFired,
			wantFired:  &fireTime,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAlert("k", "id", 71000, 70000, nil, testNow)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.apply(&a)

			if a.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", a.Status, tc.wantStatus)
			}
			switch {
			case tc.wantFired == nil && a.FiredAt != nil:
				t.Errorf("firedAt = %v, want nil", a.FiredAt)
			case tc.wantFired != nil && (a.FiredAt == nil || *a.FiredAt != *tc.wantFired):
				t.Errorf("firedAt = %v, want %v", a.FiredAt, tc.wantFired)
			}
		})
	}
}

// TestRearm covers re-arming a fired alert: the direction is re-derived from the
// supplied current price (so the alert watches its target from where the price is
// now, possibly flipping direction), the reference price and armed state are updated,
// and a price sitting on the target — or a non-positive price — is rejected.
func TestRearm(t *testing.T) {
	tests := []struct {
		name           string
		target         float64
		referencePrice float64 // price at rearm time
		wantErr        error
		wantDirection  Direction
	}{
		{name: "recomputes ABOVE when price is below target", target: 71000, referencePrice: 70000, wantDirection: DirectionAbove},
		{name: "recomputes BELOW when price is above target", target: 71000, referencePrice: 72000, wantDirection: DirectionBelow},
		{name: "rejects price equal to target", target: 71000, referencePrice: 71000, wantErr: ErrTargetEqualsReference},
		{name: "rejects non-positive price", target: 71000, referencePrice: 0, wantErr: ErrNonPositivePrice},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Start from an armed-then-fired alert; its initial direction is irrelevant
			// since Rearm re-derives it.
			a, err := NewAlert("k", "id", tc.target, 70500, nil, testNow)
			if err != nil {
				t.Fatalf("NewAlert: %v", err)
			}
			a.Fire(testNow.Add(time.Hour))

			err = a.Rearm(tc.referencePrice)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.Status != StatusArmed {
				t.Errorf("status = %q, want ARMED", a.Status)
			}
			if a.FiredAt != nil {
				t.Errorf("firedAt = %v, want nil", a.FiredAt)
			}
			if a.Direction != tc.wantDirection {
				t.Errorf("direction = %q, want %q", a.Direction, tc.wantDirection)
			}
			if a.ReferencePrice != tc.referencePrice {
				t.Errorf("referencePrice = %v, want %v", a.ReferencePrice, tc.referencePrice)
			}
		})
	}
}
