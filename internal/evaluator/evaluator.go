// internal/evaluator/evaluator.go
package evaluator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
	"github.com/nixmaldonado/btc-alerts/internal/store"
)

// PriceSource fetches the current BTC/USD spot price. *price.Client satisfies it.
type PriceSource interface {
	CurrentBTCUSD(ctx context.Context) (float64, error)
}

// Store is the narrow persistence surface the evaluator depends on.
// *store.Store satisfies it structurally; tests use an in-memory fake.
type Store interface {
	GetLastPrice(ctx context.Context) (float64, bool, error)
	PutLastPrice(ctx context.Context, price float64, now time.Time) error
	QueryArmedCrossed(ctx context.Context, dir alert.Direction, low, high float64) ([]alert.Alert, error)
	FireAlert(ctx context.Context, ownerID, id string, now time.Time) error
}

// Run performs one evaluation tick (spec §5):
//  1. read previous price,
//  2. fetch current price,
//  3. persist current price,
//  4. on the first tick (no previous), seed only and fire nothing,
//  5. query the armed alerts crossed by the move (ABOVE on a rise, BELOW on a fall),
//  6. fire each crossed alert idempotently.
//
// now is injected so the tick is deterministic. QueryArmedCrossed bounds are
// inclusive; because FireAlert is conditional on status=ARMED, re-processing an
// exact-boundary alert on a later tick is a harmless ErrNotArmed skip.
func Run(ctx context.Context, st Store, prices PriceSource, now time.Time) error {
	prev, ok, err := st.GetLastPrice(ctx)
	if err != nil {
		return fmt.Errorf("evaluator: get last price: %w", err)
	}

	cur, err := prices.CurrentBTCUSD(ctx)
	if err != nil {
		return fmt.Errorf("evaluator: fetch current price: %w", err)
	}

	if err := st.PutLastPrice(ctx, cur, now); err != nil {
		return fmt.Errorf("evaluator: put last price: %w", err)
	}

	// First tick: nothing to compare against, so just seed.
	if !ok {
		log.Printf("evaluator: seeded last price = %v (first tick)", cur)
		return nil
	}

	// An unchanged price crosses nothing.
	if cur == prev {
		return nil
	}

	// The crossed band is always [min, max]; only the direction depends on
	// whether the price rose (scan ABOVE alerts) or fell (scan BELOW).
	low, high := min(prev, cur), max(prev, cur)
	dir := alert.DirectionAbove
	if cur < prev {
		dir = alert.DirectionBelow
	}

	hits, err := st.QueryArmedCrossed(ctx, dir, low, high)
	if err != nil {
		return fmt.Errorf("evaluator: query crossed alerts: %w", err)
	}

	for _, hit := range hits {
		if err := st.FireAlert(ctx, hit.OwnerID, hit.ID, now); err != nil {
			// Lost the race (already fired/disarmed): benign, keep going.
			if errors.Is(err, store.ErrNotArmed) {
				log.Printf("evaluator: alert %s/%s no longer armed, skipping", hit.OwnerID, hit.ID)
				continue
			}
			return fmt.Errorf("evaluator: fire alert %s/%s: %w", hit.OwnerID, hit.ID, err)
		}
	}
	return nil
}
