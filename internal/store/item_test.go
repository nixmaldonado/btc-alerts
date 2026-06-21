package store

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
)

var marshalNow = time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

func ptrF(v float64) *float64 { return &v }

// TestToItem_SparseGSIAttributes checks the sparse-index rule directly on the item map:
// armed alerts carry gsi_pk/gsi_sk, fired alerts must not. Asserting specific KEYS keeps us
// out of diffing raw map[string]types.AttributeValue, whose interface values are awkward to compare.
func TestToItem_SparseGSIAttributes(t *testing.T) {
	armed, err := alert.NewAlert("key123", "id1", 71000, 70000, nil, marshalNow)
	if err != nil {
		t.Fatalf("NewAlert: %v", err)
	}
	fired, _ := alert.NewAlert("key123", "id1", 71000, 70000, nil, marshalNow)
	fired.Fire(marshalNow.Add(time.Hour))

	tests := []struct {
		name        string
		alert       alert.Alert
		wantGSI     bool // gsi_pk/gsi_sk present?
		wantFiredAt bool // firedAt present?
	}{
		{name: "armed has sparse gsi attrs", alert: armed, wantGSI: true, wantFiredAt: false},
		{name: "fired omits sparse gsi attrs", alert: fired, wantGSI: false, wantFiredAt: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := toItem(tt.alert)

			if _, ok := it["gsi_pk"]; ok != tt.wantGSI {
				t.Errorf("gsi_pk present = %v, want %v", ok, tt.wantGSI)
			}
			if _, ok := it["gsi_sk"]; ok != tt.wantGSI {
				t.Errorf("gsi_sk present = %v, want %v", ok, tt.wantGSI)
			}
			if _, ok := it["firedAt"]; ok != tt.wantFiredAt {
				t.Errorf("firedAt present = %v, want %v", ok, tt.wantFiredAt)
			}
			// pct is always absent here (no alert sets it).
			if _, ok := it["pct"]; ok {
				t.Errorf("pct present but alert has nil Pct")
			}
		})
	}
}

// TestRoundTrip checks fromItem(toItem(a)) reproduces the original Alert for every shape.
// cmp.Diff compares whole values; it uses time.Time's Equal method, so createdAt/firedAt
// round-trip cleanly.
func TestRoundTrip(t *testing.T) {
	armed, _ := alert.NewAlert("key123", "id1", 71000, 70000, nil, marshalNow)

	fired, _ := alert.NewAlert("key123", "id1", 65000, 70000, nil, marshalNow)
	fired.Fire(marshalNow.Add(2 * time.Hour))

	pctSet, _ := alert.NewAlert("key123", "id1", 77000, 70000, ptrF(0.10), marshalNow)

	tests := []struct {
		name string
		want alert.Alert
	}{
		{name: "armed", want: armed},
		{name: "fired", want: fired},
		{name: "pct set", want: pctSet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fromItem(toItem(tt.want))
			if err != nil {
				t.Fatalf("fromItem: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("alert mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
