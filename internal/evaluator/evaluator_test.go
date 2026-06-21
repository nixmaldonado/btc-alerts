// internal/evaluator/evaluator_test.go
package evaluator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
	"github.com/nixmaldonado/btc-alerts/internal/store"
)

var testNow = time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

// queryCall records one QueryArmedCrossed invocation.
type queryCall struct {
	dir       alert.Direction
	low, high float64
}

// fireCall records one FireAlert invocation.
type fireCall struct {
	ownerID, id string
}

// fakeStore is a fully in-memory Store for deterministic evaluator tests.
type fakeStore struct {
	lastPrice float64
	lastOK    bool
	getErr    error
	putErr    error
	queryErr  error
	hits      []alert.Ref      // returned from QueryArmedCrossed
	fireErrs  map[string]error // keyed by id -> error returned from FireAlert
	puts      []float64        // prices passed to PutLastPrice
	queries   []queryCall
	fires     []fireCall
}

func (f *fakeStore) GetLastPrice(ctx context.Context) (float64, bool, error) {
	return f.lastPrice, f.lastOK, f.getErr
}

func (f *fakeStore) PutLastPrice(ctx context.Context, price float64, now time.Time) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.puts = append(f.puts, price)
	return nil
}

func (f *fakeStore) QueryArmedCrossed(ctx context.Context, dir alert.Direction, low, high float64) ([]alert.Ref, error) {
	f.queries = append(f.queries, queryCall{dir: dir, low: low, high: high})
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return f.hits, nil
}

func (f *fakeStore) FireAlert(ctx context.Context, ownerID, id string, now time.Time) error {
	f.fires = append(f.fires, fireCall{ownerID: ownerID, id: id})
	if f.fireErrs != nil {
		return f.fireErrs[id]
	}
	return nil
}

// fakePrices returns a fixed current price (or an error).
type fakePrices struct {
	price float64
	err   error
}

func (p fakePrices) CurrentBTCUSD(ctx context.Context) (float64, error) {
	return p.price, p.err
}

// TestRun_Orchestration covers the per-tick flow: first-tick seed, rise, fall,
// equal, the ErrNotArmed skip, and error propagation. The recorded queries and
// fires captured by fakeStore are whole-value compared with cmp.Diff; wantErr
// (nil = success) is asserted with errors.Is so wrapped store/price errors match.
func TestRun_Orchestration(t *testing.T) {
	boom := errors.New("boom")

	tests := []struct {
		name string

		// fakeStore setup.
		lastPrice float64
		lastOK    bool
		getErr    error
		putErr    error
		queryErr  error
		hits      []alert.Ref
		fireErrs  map[string]error

		// price source.
		curPrice float64
		priceErr error

		// expectations.
		wantPuts    []float64
		wantQueries []queryCall
		wantFires   []fireCall
		wantErr     error // nil = success; matched with errors.Is
	}{
		{
			name:        "first tick seeds and fires nothing",
			lastOK:      false,
			curPrice:    70000,
			wantPuts:    []float64{70000},
			wantQueries: nil,
			wantFires:   nil,
		},
		{
			name:      "rise queries ABOVE (prev,cur) and fires hits in order",
			lastPrice: 70000, lastOK: true,
			hits: []alert.Ref{
				{OwnerID: "k1", ID: "a1"},
				{OwnerID: "k2", ID: "a2"},
			},
			curPrice:    71000,
			wantPuts:    []float64{71000},
			wantQueries: []queryCall{{dir: alert.DirectionAbove, low: 70000, high: 71000}},
			wantFires:   []fireCall{{"k1", "a1"}, {"k2", "a2"}},
		},
		{
			name:      "fall queries BELOW (cur,prev)",
			lastPrice: 70000, lastOK: true,
			curPrice:    69000,
			wantPuts:    []float64{69000},
			wantQueries: []queryCall{{dir: alert.DirectionBelow, low: 69000, high: 70000}},
			wantFires:   nil,
		},
		{
			name:      "equal price fires nothing",
			lastPrice: 70000, lastOK: true,
			curPrice:    70000,
			wantPuts:    []float64{70000},
			wantQueries: nil,
			wantFires:   nil,
		},
		{
			name:      "ErrNotArmed is skipped, siblings still fire",
			lastPrice: 70000, lastOK: true,
			hits: []alert.Ref{
				{OwnerID: "k1", ID: "a1"},
				{OwnerID: "k2", ID: "a2"}, // already fired concurrently
				{OwnerID: "k3", ID: "a3"},
			},
			fireErrs:    map[string]error{"a2": store.ErrNotArmed},
			curPrice:    71000,
			wantPuts:    []float64{71000},
			wantQueries: []queryCall{{dir: alert.DirectionAbove, low: 70000, high: 71000}},
			wantFires:   []fireCall{{"k1", "a1"}, {"k2", "a2"}, {"k3", "a3"}},
		},
		{
			name:      "other fire error aborts the tick",
			lastPrice: 70000, lastOK: true,
			hits:     []alert.Ref{{OwnerID: "k1", ID: "a1"}},
			fireErrs: map[string]error{"a1": boom},
			curPrice: 71000,
			wantErr:  boom,
		},
		{
			name:     "GetLastPrice error propagates",
			getErr:   boom,
			curPrice: 1,
			wantErr:  boom,
		},
		{
			name:      "CurrentBTCUSD error propagates",
			lastPrice: 1, lastOK: true,
			priceErr: boom,
			wantErr:  boom,
		},
		{
			name:      "PutLastPrice error propagates",
			lastPrice: 1, lastOK: true,
			putErr:   boom,
			curPrice: 2,
			wantErr:  boom,
		},
		{
			name:      "QueryArmedCrossed error propagates",
			lastPrice: 1, lastOK: true,
			queryErr: boom,
			curPrice: 2,
			wantErr:  boom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &fakeStore{
				lastPrice: tt.lastPrice,
				lastOK:    tt.lastOK,
				getErr:    tt.getErr,
				putErr:    tt.putErr,
				queryErr:  tt.queryErr,
				hits:      tt.hits,
				fireErrs:  tt.fireErrs,
			}
			prices := fakePrices{price: tt.curPrice, err: tt.priceErr}

			err := Run(context.Background(), st, prices, testNow)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want errors.Is %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.wantPuts, st.puts); diff != "" {
				t.Errorf("puts mismatch (-want +got):\n%s", diff)
			}
			// cmp needs access to queryCall's unexported fields.
			if diff := cmp.Diff(tt.wantQueries, st.queries, cmp.AllowUnexported(queryCall{})); diff != "" {
				t.Errorf("queries mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantFires, st.fires, cmp.AllowUnexported(fireCall{})); diff != "" {
				t.Errorf("fires mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
