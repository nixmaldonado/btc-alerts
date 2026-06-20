package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/go-cmp/cmp"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
)

// To run these tests, start DynamoDB Local and point the suite at it:
//
//	docker run -p 8000:8000 amazon/dynamodb-local
//	DYNAMODB_ENDPOINT=http://localhost:8000 go test ./internal/store/
//
// With DYNAMODB_ENDPOINT unset every integration test t.Skips, so `go test ./...`
// stays green without Docker.

var fixedNow = time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

// newTestClient builds a *dynamodb.Client pointed at DynamoDB Local, or skips the test
// if DYNAMODB_ENDPOINT is unset. Credentials and region are dummy; Local ignores them.
func newTestClient(t *testing.T) *dynamodb.Client {
	t.Helper()
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set; skipping DynamoDB Local integration test")
	}
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("dummy", "dummy", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

// newTestStore creates a uniquely named `alerts` table + GSI for this run and registers
// teardown, returning a Store bound to it. Skips when DYNAMODB_ENDPOINT is unset.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	client := newTestClient(t)
	ctx := context.Background()
	table := fmt.Sprintf("alerts-test-%d", time.Now().UnixNano())

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String(table),
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String(attrPK), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String(attrSK), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String(attrGSIPK), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String(attrGSISK), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String(attrPK), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String(attrSK), KeyType: types.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: aws.String(GSIName),
				KeySchema: []types.KeySchemaElement{
					{AttributeName: aws.String(attrGSIPK), KeyType: types.KeyTypeHash},
					{AttributeName: aws.String(attrGSISK), KeyType: types.KeyTypeRange},
				},
				Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll},
			},
		},
	})
	if err != nil {
		t.Fatalf("create table %s: %v", table, err)
	}

	waiter := dynamodb.NewTableExistsWaiter(client)
	if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 30*time.Second); err != nil {
		t.Fatalf("wait for table %s: %v", table, err)
	}

	t.Cleanup(func() {
		_, _ = client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(table)})
	})

	return New(client, table)
}

// mustPut builds an armed alert and stores it, failing the test on error.
func mustPut(t *testing.T, s *Store, owner, id string, target, reference float64) alert.Alert {
	t.Helper()
	a, err := alert.NewAlert(owner, id, "user@example.com", target, reference, nil, fixedNow)
	if err != nil {
		t.Fatalf("NewAlert: %v", err)
	}
	if err := s.PutAlert(context.Background(), a); err != nil {
		t.Fatalf("PutAlert: %v", err)
	}
	return a
}

// TestGetAlert consolidates the stored-alert and missing-alert scenarios into one
// table. Each subtest provisions its own store so cases stay independent; seed (when
// present) writes the fixture and returns the want value compared via cmp.Diff.
func TestGetAlert(t *testing.T) {
	tests := []struct {
		name    string
		seed    func(t *testing.T, s *Store) alert.Alert // optional setup; returns the want
		ownerID string
		id      string
		wantErr error // nil = expect success
	}{
		{
			name:    "returns stored alert",
			seed:    func(t *testing.T, s *Store) alert.Alert { return mustPut(t, s, "key123", "id1", 71000, 70000) },
			ownerID: "key123",
			id:      "id1",
		},
		{
			name:    "missing returns ErrNotFound",
			ownerID: "key123",
			id:      "missing",
			wantErr: ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			var want alert.Alert
			if tt.seed != nil {
				want = tt.seed(t, s)
			}
			got, err := s.GetAlert(context.Background(), tt.ownerID, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("alert mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestListAlerts is table-driven over the owner whose alerts we list. seed populates the
// table and returns the slice expected back; ListAlerts must return only that owner's
// alerts in SK (ALERT#<id>) order.
func TestListAlerts(t *testing.T) {
	tests := []struct {
		name    string
		seed    func(t *testing.T, s *Store) []alert.Alert
		ownerID string
	}{
		{
			name: "returns only the owner's alerts in SK order",
			seed: func(t *testing.T, s *Store) []alert.Alert {
				// Query returns OWNER#key123 items in SK (ALERT#<id>) order: id1 then id2.
				want1 := mustPut(t, s, "key123", "id1", 71000, 70000)
				want2 := mustPut(t, s, "key123", "id2", 65000, 70000)
				mustPut(t, s, "other", "id3", 80000, 70000) // different owner; must not appear
				return []alert.Alert{want1, want2}
			},
			ownerID: "key123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			want := tt.seed(t, s)
			got, err := s.ListAlerts(context.Background(), tt.ownerID)
			if err != nil {
				t.Fatalf("ListAlerts: %v", err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("alerts mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDeleteAlert consolidates the existing-then-gone and missing cases. seed (when
// present) stores a fixture before the delete; after a successful delete the case
// re-asserts GetAlert now returns ErrNotFound.
func TestDeleteAlert(t *testing.T) {
	tests := []struct {
		name    string
		seed    func(t *testing.T, s *Store)
		ownerID string
		id      string
		wantErr error // nil = expect success
	}{
		{
			name:    "deletes existing then GetAlert is ErrNotFound",
			seed:    func(t *testing.T, s *Store) { mustPut(t, s, "key123", "id1", 71000, 70000) },
			ownerID: "key123",
			id:      "id1",
		},
		{
			name:    "missing returns ErrNotFound",
			ownerID: "key123",
			id:      "missing",
			wantErr: ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			if tt.seed != nil {
				tt.seed(t, s)
			}
			err := s.DeleteAlert(ctx, tt.ownerID, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("DeleteAlert err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if _, err := s.GetAlert(ctx, tt.ownerID, tt.id); !errors.Is(err, ErrNotFound) {
				t.Errorf("after delete GetAlert err = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestRearmAlert consolidates the restore-and-reindex and missing cases. seed (when
// present) writes a fired alert (no gsi_pk/gsi_sk) and returns the want value — the
// independently-rearmed copy. On success the subtest checks the returned alert and that
// it re-enters the sparse GSI range query.
//
// The success case depends on QueryArmedCrossed (Task 5); run it only after Task 5, or
// -run the "missing" case here first.
func TestRearmAlert(t *testing.T) {
	tests := []struct {
		name    string
		seed    func(t *testing.T, s *Store) alert.Alert // optional setup; returns the want
		ownerID string
		id      string
		wantErr error // nil = expect success
	}{
		{
			name: "rearms fired alert and restores sparse index",
			seed: func(t *testing.T, s *Store) alert.Alert {
				// Store a fired alert directly so it has no gsi_pk/gsi_sk.
				a, _ := alert.NewAlert("key123", "id1", "user@example.com", 71000, 70000, nil, fixedNow)
				a.Fire(fixedNow.Add(time.Hour))
				if err := s.PutAlert(context.Background(), a); err != nil {
					t.Fatalf("PutAlert(fired): %v", err)
				}
				// Expected: ARMED again, firedAt cleared — built from an independent copy.
				want := a
				want.Rearm()
				return want
			},
			ownerID: "key123",
			id:      "id1",
		},
		{
			name:    "missing returns ErrNotFound",
			ownerID: "key123",
			id:      "missing",
			wantErr: ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			var want alert.Alert
			if tt.seed != nil {
				want = tt.seed(t, s)
			}
			got, err := s.RearmAlert(ctx, tt.ownerID, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RearmAlert err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("rearmed alert mismatch (-want +got):\n%s", diff)
			}
			// The rearmed alert must reappear in the sparse GSI range query.
			hits, err := s.QueryArmedCrossed(ctx, alert.DirectionAbove, 70500, 71500)
			if err != nil {
				t.Fatalf("QueryArmedCrossed: %v", err)
			}
			if diff := cmp.Diff([]alert.Alert{want}, hits); diff != "" {
				t.Errorf("after rearm QueryArmedCrossed mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLastPrice consolidates the absent-singleton and written-price cases. write is the
// price to PutLastPrice first (skipped when wantOK is false, leaving the singleton absent);
// GetLastPrice must then report the expected ok flag and price.
func TestLastPrice(t *testing.T) {
	tests := []struct {
		name      string
		write     *float64 // nil = don't write the singleton
		wantOK    bool
		wantPrice float64
	}{
		{name: "absent returns ok=false", write: nil, wantOK: false},
		{name: "returns the written price", write: ptrF(70123.45), wantOK: true, wantPrice: 70123.45},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			if tt.write != nil {
				if err := s.PutLastPrice(ctx, *tt.write, fixedNow); err != nil {
					t.Fatalf("PutLastPrice: %v", err)
				}
			}
			price, ok, err := s.GetLastPrice(ctx)
			if err != nil {
				t.Fatalf("GetLastPrice: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if price != tt.wantPrice {
				t.Errorf("price = %v, want %v", price, tt.wantPrice)
			}
		})
	}
}

// TestQueryArmedCrossed is table-driven over the GSI range filter. seed populates the
// table (the same fixtures for every case) and the table enumerates direction/low/high
// inputs with the alert IDs each query must return, in ascending-target (gsi_sk) order.
// This exercises in-range, out-of-range, wrong-direction, and inclusive-boundary behavior.
func TestQueryArmedCrossed(t *testing.T) {
	// seed writes one armed alert per id and returns them keyed by id so each case can
	// build its want slice. gsi_sk is the zero-padded target, so the GSI returns matches
	// in ascending target order.
	seed := func(t *testing.T, s *Store) map[string]alert.Alert {
		return map[string]alert.Alert{
			"above-low":  mustPut(t, s, "key123", "above-low", 70500, 70000),  // ABOVE @ 70500
			"above-high": mustPut(t, s, "key123", "above-high", 71000, 70000), // ABOVE @ 71000
			"above-out":  mustPut(t, s, "key123", "above-out", 72000, 70000),  // ABOVE @ 72000
			"below":      mustPut(t, s, "key123", "below", 69000, 70000),      // BELOW @ 69000
		}
	}

	tests := []struct {
		name    string
		dir     alert.Direction
		low     float64
		high    float64
		wantIDs []string // expected hit ids, in ascending-target order
	}{
		{name: "in-range armed alert returned", dir: alert.DirectionAbove, low: 70400, high: 70600, wantIDs: []string{"above-low"}},
		{name: "out-of-range excluded", dir: alert.DirectionAbove, low: 73000, high: 74000, wantIDs: nil},
		{name: "wrong-direction excluded", dir: alert.DirectionBelow, low: 70400, high: 71100, wantIDs: nil},
		{name: "inclusive boundary included", dir: alert.DirectionAbove, low: 70500, high: 71000, wantIDs: []string{"above-low", "above-high"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			fixtures := seed(t, s)
			var want []alert.Alert
			for _, id := range tt.wantIDs {
				want = append(want, fixtures[id])
			}
			hits, err := s.QueryArmedCrossed(context.Background(), tt.dir, tt.low, tt.high)
			if err != nil {
				t.Fatalf("QueryArmedCrossed: %v", err)
			}
			if diff := cmp.Diff(want, hits); diff != "" {
				t.Errorf("crossed alerts mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestFireAlert consolidates the idempotent-fire and missing cases. The fire/second-fire
// idempotency is inherently ordered, so it lives in a single subtest that fires, asserts
// the FIRED item and its removal from the sparse GSI, then fires again expecting ErrNotArmed
// with the item unchanged. The missing case is a standalone row.
func TestFireAlert(t *testing.T) {
	t.Run("fires armed alert, drops it from the GSI, and second fire returns ErrNotArmed", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		a := mustPut(t, s, "key123", "id1", 71000, 70000) // ABOVE @ 71000

		// Expected post-fire alert: the domain Fire transition applied to a copy.
		want := a
		want.Fire(fixedNow.Add(time.Hour))

		// First fire succeeds.
		if err := s.FireAlert(ctx, "key123", "id1", fixedNow.Add(time.Hour)); err != nil {
			t.Fatalf("first FireAlert: %v", err)
		}
		fired, err := s.GetAlert(ctx, "key123", "id1")
		if err != nil {
			t.Fatalf("GetAlert after fire: %v", err)
		}
		if diff := cmp.Diff(want, fired); diff != "" {
			t.Errorf("fired alert mismatch (-want +got):\n%s", diff)
		}

		// After firing, the alert is gone from the sparse index.
		hits, err := s.QueryArmedCrossed(ctx, alert.DirectionAbove, 70500, 71500)
		if err != nil {
			t.Fatalf("QueryArmedCrossed: %v", err)
		}
		for _, h := range hits {
			if h.ID == "id1" {
				t.Errorf("fired alert id1 still in GSI; sparse index must drop it")
			}
		}

		// Second fire is a no-op: ErrNotArmed and the item is unchanged.
		err = s.FireAlert(ctx, "key123", "id1", fixedNow.Add(2*time.Hour))
		if !errors.Is(err, ErrNotArmed) {
			t.Fatalf("second FireAlert err = %v, want ErrNotArmed", err)
		}
		again, err := s.GetAlert(ctx, "key123", "id1")
		if err != nil {
			t.Fatalf("GetAlert after second fire: %v", err)
		}
		if diff := cmp.Diff(want, again); diff != "" {
			t.Errorf("alert changed on second fire (-want +got):\n%s", diff)
		}
	})

	t.Run("missing returns ErrNotArmed", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.FireAlert(context.Background(), "key123", "missing", fixedNow); !errors.Is(err, ErrNotArmed) {
			t.Errorf("FireAlert err = %v, want ErrNotArmed", err)
		}
	})
}
