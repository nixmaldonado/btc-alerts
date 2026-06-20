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
		email         string
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
			email:         "user@example.com",
			target:        71000,
			reference:     70000,
			wantDirection: DirectionAbove,
		},
		{
			name:          "direction is BELOW when target is under reference",
			ownerID:       "key123",
			id:            "id2",
			email:         "user@example.com",
			target:        65000,
			reference:     70000,
			wantDirection: DirectionBelow,
		},
		{
			name:      "rejects target equal to reference (direction undefined)",
			ownerID:   "key123",
			id:        "id3",
			email:     "user@example.com",
			target:    70000,
			reference: 70000,
			wantErr:   ErrTargetEqualsReference,
		},
		{
			name:      "rejects zero target price",
			id:        "id",
			email:     "user@example.com",
			target:    0,
			reference: 70000,
			wantErr:   ErrNonPositivePrice,
		},
		{
			name:      "rejects negative reference price",
			id:        "id",
			email:     "user@example.com",
			target:    71000,
			reference: -1,
			wantErr:   ErrNonPositivePrice,
		},
		{
			name:      "rejects empty email",
			id:        "id",
			email:     "",
			target:    71000,
			reference: 70000,
			wantErr:   ErrEmptyEmail,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAlert(tc.ownerID, tc.id, tc.email, tc.target, tc.reference, tc.pct, testNow)

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
		{
			name:       "Rearm resets to ARMED and clears the fired timestamp",
			apply:      func(a *Alert) { a.Fire(fireTime); a.Rearm() },
			wantStatus: StatusArmed,
			wantFired:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAlert("k", "id", "user@example.com", 71000, 70000, nil, testNow)
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
