package alert

import "testing"

func TestKeyConstructors(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "OwnerPK prefixes the owner id", got: OwnerPK("key123"), want: "OWNER#key123"},
		{name: "AlertSK prefixes the alert id", got: AlertSK("id1"), want: "ALERT#id1"},
		{name: "StatePK is the singleton price key", got: StatePK(), want: "STATE#PRICE"},
		{name: "StateSK is the singleton price key", got: StateSK(), want: "STATE#PRICE"},
		{name: "GSIPK splits armed alerts by ABOVE direction", got: GSIPK(DirectionAbove), want: "ARMED#ABOVE"},
		{name: "GSIPK splits armed alerts by BELOW direction", got: GSIPK(DirectionBelow), want: "ARMED#BELOW"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestGSISK_FixedWidth(t *testing.T) {
	tests := []struct {
		name  string
		price float64
		want  string
	}{
		{name: "pads a five-digit price to fixed width", price: 70000, want: "00070000.00"},
		{name: "pads a four-digit price to the same width", price: 9000, want: "00009000.00"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GSISK(tc.price)
			if got != tc.want {
				t.Errorf("GSISK(%v) = %q, want %q", tc.price, got, tc.want)
			}
			if len(got) != len(tc.want) {
				t.Errorf("GSISK(%v) width = %d, want %d", tc.price, len(got), len(tc.want))
			}
		})
	}
}

// The whole point of zero-padding: lexicographic order must equal numeric order,
// even across digit-count boundaries (9000 < 10000).
func TestGSISK_LexicographicMatchesNumeric(t *testing.T) {
	tests := []struct {
		name string
		lo   float64
		hi   float64
	}{
		{name: "crosses a digit-count boundary (9000 < 10000)", lo: 9000, hi: 10000},
		{name: "same digit count (70000 < 71000)", lo: 70000, hi: 71000},
		{name: "crosses the thousands boundary (999.99 < 1000.00)", lo: 999.99, hi: 1000.00},
		{name: "sub-dollar prices (0.01 < 0.02)", lo: 0.01, hi: 0.02},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if GSISK(tc.lo) >= GSISK(tc.hi) {
				t.Errorf("GSISK(%v)=%q should sort before GSISK(%v)=%q", tc.lo, GSISK(tc.lo), tc.hi, GSISK(tc.hi))
			}
		})
	}
}
