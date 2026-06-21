package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
	"github.com/nixmaldonado/btc-alerts/internal/store"
)

var testNow = time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

const testID = "fixed-id-1"

// fakeStore is an in-memory implementation of the api.Store interface.
// It needs no AWS/DynamoDB, so every handler test is hermetic.
type fakeStore struct {
	alerts    map[string]alert.Alert // key: ownerID + "|" + id
	emails    map[string]string      // key: ownerID -> profile email
	lastPrice float64
	priceOK   bool
	putErr    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{alerts: map[string]alert.Alert{}, emails: map[string]string{}}
}

func key(ownerID, id string) string { return ownerID + "|" + id }

func (f *fakeStore) PutAlert(_ context.Context, a alert.Alert) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.alerts[key(a.OwnerID, a.ID)] = a
	return nil
}

func (f *fakeStore) ListAlerts(_ context.Context, ownerID string) ([]alert.Alert, error) {
	out := []alert.Alert{}
	for _, a := range f.alerts {
		if a.OwnerID == ownerID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) GetAlert(_ context.Context, ownerID, id string) (alert.Alert, error) {
	a, ok := f.alerts[key(ownerID, id)]
	if !ok {
		return alert.Alert{}, store.ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) DeleteAlert(_ context.Context, ownerID, id string) error {
	if _, ok := f.alerts[key(ownerID, id)]; !ok {
		return store.ErrNotFound
	}
	delete(f.alerts, key(ownerID, id))
	return nil
}

func (f *fakeStore) RearmAlert(_ context.Context, ownerID, id string) (alert.Alert, error) {
	a, ok := f.alerts[key(ownerID, id)]
	if !ok {
		return alert.Alert{}, store.ErrNotFound
	}
	a.Rearm()
	f.alerts[key(ownerID, id)] = a
	return a, nil
}

func (f *fakeStore) GetLastPrice(_ context.Context) (float64, bool, error) {
	return f.lastPrice, f.priceOK, nil
}

func (f *fakeStore) GetOwnerEmail(_ context.Context, ownerID string) (string, bool, error) {
	email, ok := f.emails[ownerID]
	return email, ok, nil
}

func (f *fakeStore) PutOwnerEmail(_ context.Context, ownerID, email string) error {
	f.emails[ownerID] = email
	return nil
}

// newTestRouter wires the fake with a fixed clock and id and returns the mux
// router under test. The real Lambda resolves the owner from the API-Gateway
// context; tests inject it directly with withOwner (see do).
func newTestRouter(f *fakeStore) http.Handler {
	return New(f, func() time.Time { return testNow }, func() string { return testID }).Router()
}

// do drives the router with an httptest request. An empty owner means
// "unauthenticated" — no owner is placed on the context, so requireOwner rejects it.
func do(router http.Handler, method, target, owner, body string) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	if owner != "" {
		req = req.WithContext(withOwner(req.Context(), owner))
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// TestRoute_Dispatch covers auth + unknown-route dispatch as a table. Each case
// is a scalar status check, so direct comparison (not cmp.Diff) is the right tool.
func TestRoute_Dispatch(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		owner      string
		wantStatus int
	}{
		{name: "missing api key", method: "GET", target: "/alerts", owner: "", wantStatus: 401},
		{name: "unknown route", method: "PUT", target: "/nope", owner: "key1", wantStatus: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := do(newTestRouter(newFakeStore()), tt.method, tt.target, tt.owner, "")
			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}

// Compile-time proof that the real store satisfies the narrow interface.
// (store.Store is implemented in Plan 2.)
var _ Store = (*store.Store)(nil)

// pctPtr is a tiny helper so table rows can express an optional pct inline.
func pctPtr(v float64) *float64 { return &v }

// TestCreateAlert_Success drives the happy paths (absolute target, pct→absolute)
// as a table. The success cases compare the whole decoded alertResponse with
// cmp.Diff, and the stored alert with cmp.Diff, so the assertion is a single
// whole-value check rather than a pile of per-field scalar comparisons.
func TestCreateAlert_Success(t *testing.T) {
	tests := []struct {
		name  string
		owner string
		body  string
		want  alertResponse // expected decoded response body
	}{
		{
			name:  "absolute target",
			owner: "key1",
			body:  `{"targetPrice":71000}`,
			want: alertResponse{
				ID:             testID,
				Status:         "ARMED",
				Direction:      "ABOVE",
				TargetPrice:    71000,
				ReferencePrice: 70000,
				Pct:            nil,
				CreatedAt:      testNow,
			},
		},
		{
			name:  "pct resolves against last price",
			owner: "key1",
			body:  `{"pct":0.10}`,
			want: alertResponse{
				ID:             testID,
				Status:         "ARMED",
				Direction:      "ABOVE",
				TargetPrice:    77000, // 70000 * (1 + 0.10)
				ReferencePrice: 70000,
				Pct:            pctPtr(0.10),
				CreatedAt:      testNow,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStore()
			f.lastPrice, f.priceOK = 70000, true
			f.emails[tt.owner] = "u@example.com" // profile email is required to create

			rr := do(newTestRouter(f), "POST", "/alerts", tt.owner, tt.body)
			if rr.Code != 201 {
				t.Fatalf("status = %d, want 201 (body=%s)", rr.Code, rr.Body.String())
			}
			got := decodeAlert(t, rr.Body.String())
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("response alert mismatch (-want +got):\n%s", diff)
			}

			// The alert persisted into the store must match the response.
			if len(f.alerts) != 1 {
				t.Fatalf("store has %d alerts, want 1 (PutAlert not called?)", len(f.alerts))
			}
			stored := f.alerts[key(tt.owner, testID)]
			if diff := cmp.Diff(tt.want, newAlertResponse(stored)); diff != "" {
				t.Errorf("stored alert mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCreateAlert_ValidationErrors models each rejection scenario as a row with
// the expected HTTP status. These are scalar status checks, so direct comparison
// is correct; cmp.Diff is reserved for whole-value alert comparisons above.
func TestCreateAlert_ValidationErrors(t *testing.T) {
	tests := []struct {
		name         string
		priceOK      bool
		profileEmail string // "" = no profile set for the tenant
		body         string
		wantStatus   int
	}{
		{name: "both target and pct", priceOK: true, profileEmail: "u@example.com", body: `{"targetPrice":71000,"pct":0.1}`, wantStatus: 400},
		{name: "neither target nor pct", priceOK: true, profileEmail: "u@example.com", body: `{}`, wantStatus: 400},
		{name: "no profile email set", priceOK: true, profileEmail: "", body: `{"targetPrice":71000}`, wantStatus: 409},
		{name: "malformed json", priceOK: true, profileEmail: "u@example.com", body: `{not json`, wantStatus: 400},
		{name: "target equals reference", priceOK: true, profileEmail: "u@example.com", body: `{"targetPrice":70000}`, wantStatus: 400},
		{name: "no price observed yet", priceOK: false, profileEmail: "u@example.com", body: `{"targetPrice":71000}`, wantStatus: 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStore()
			f.lastPrice, f.priceOK = 70000, tt.priceOK
			if tt.profileEmail != "" {
				f.emails["key1"] = tt.profileEmail
			}

			rr := do(newTestRouter(f), "POST", "/alerts", "key1", tt.body)
			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if len(f.alerts) != 0 {
				t.Errorf("store has %d alerts, want 0 on failure", len(f.alerts))
			}
		})
	}
}

func seedAlert(t *testing.T, f *fakeStore, ownerID, id string) alert.Alert {
	t.Helper()
	a, err := alert.NewAlert(ownerID, id, 71000, 70000, nil, testNow)
	if err != nil {
		t.Fatalf("seed NewAlert: %v", err)
	}
	f.alerts[key(ownerID, id)] = a
	return a
}

// seedAlertFor builds (but does not store) an expected alert for list assertions.
func seedAlertFor(t *testing.T, ownerID, id string) alert.Alert {
	t.Helper()
	a, err := alert.NewAlert(ownerID, id, 71000, 70000, nil, testNow)
	if err != nil {
		t.Fatalf("expected NewAlert: %v", err)
	}
	return a
}

// sortByID gives list comparisons a stable order (the fake store iterates a map).
func sortByID(rs []alertResponse) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
}

// TestListAlerts compares the whole decoded list against the expected slice of
// alertResponse with cmp.Diff — a single whole-value assertion that proves both
// caller-scoping (no "other" tenant leakage) and the empty-list shape.
func TestListAlerts(t *testing.T) {
	a1 := newAlertResponse(seedAlertFor(t, "key1", "a1"))
	a2 := newAlertResponse(seedAlertFor(t, "key1", "a2"))

	tests := []struct {
		name  string
		seed  func(f *fakeStore)
		owner string
		want  []alertResponse
	}{
		{
			name: "returns only caller's alerts",
			seed: func(f *fakeStore) {
				seedAlert(t, f, "key1", "a1")
				seedAlert(t, f, "key1", "a2")
				seedAlert(t, f, "other", "b1")
			},
			owner: "key1",
			want:  []alertResponse{a1, a2},
		},
		{
			name:  "empty returns json array not null",
			seed:  func(f *fakeStore) {},
			owner: "key1",
			want:  []alertResponse{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStore()
			tt.seed(f)

			rr := do(newTestRouter(f), "GET", "/alerts", tt.owner, "")
			if rr.Code != 200 {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			// Guard: must be a JSON array, never null, even when empty.
			if rr.Body.String() == "null" {
				t.Fatalf("body = null, want a JSON array")
			}
			var got []alertResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("bad list JSON %q: %v", rr.Body.String(), err)
			}
			sortByID(got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("list mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDeleteAlert(t *testing.T) {
	tests := []struct {
		name        string
		id          string // path segment
		seedID      string // id to seed; "" means seed nothing
		wantStatus  int
		wantErr     error // domain error the store path should surface (nil = success)
		wantDeleted bool  // whether the seeded alert should be gone afterwards
	}{
		{name: "success", id: "a1", seedID: "a1", wantStatus: 204, wantErr: nil, wantDeleted: true},
		{name: "not found", id: "missing", seedID: "", wantStatus: 404, wantErr: store.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStore()
			if tt.seedID != "" {
				seedAlert(t, f, "key1", tt.seedID)
			}

			rr := do(newTestRouter(f), "DELETE", "/alerts/"+tt.id, "key1", "")
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			// On the not-found path, assert the domain error directly via errors.Is.
			if tt.wantErr != nil {
				if _, gotErr := f.GetAlert(context.Background(), "key1", tt.id); !errors.Is(gotErr, tt.wantErr) {
					t.Errorf("store error = %v, want %v", gotErr, tt.wantErr)
				}
			}
			if tt.wantStatus == 204 && rr.Body.Len() != 0 {
				t.Errorf("body = %q, want empty", rr.Body.String())
			}
			if tt.wantDeleted && len(f.alerts) != 0 {
				t.Errorf("alert not deleted from store")
			}
		})
	}
}

func TestRearmAlert(t *testing.T) {
	tests := []struct {
		name       string
		id         string // path segment
		seedFired  bool   // seed a FIRED "a1" the route should re-arm
		wantStatus int
		wantErr    error          // domain error on the store path (nil = success)
		want       *alertResponse // expected decoded body on success (nil = no body check)
	}{
		{
			name:       "returns armed alert",
			id:         "a1",
			seedFired:  true,
			wantStatus: 200,
			wantErr:    nil,
			want: &alertResponse{
				ID:             "a1",
				Status:         "ARMED",
				Direction:      "ABOVE",
				TargetPrice:    71000,
				ReferencePrice: 70000,
				Pct:            nil,
				CreatedAt:      testNow,
				FiredAt:        nil, // cleared by rearm
			},
		},
		{
			name:       "not found",
			id:         "missing",
			seedFired:  false,
			wantStatus: 404,
			wantErr:    store.ErrNotFound,
			want:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStore()
			if tt.seedFired {
				a := seedAlert(t, f, "key1", "a1")
				a.Fire(testNow.Add(time.Hour)) // FIRED in the store
				f.alerts[key("key1", "a1")] = a
			}

			rr := do(newTestRouter(f), "POST", "/alerts/"+tt.id+"/rearm", "key1", "")
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantErr != nil {
				if _, gotErr := f.GetAlert(context.Background(), "key1", tt.id); !errors.Is(gotErr, tt.wantErr) {
					t.Errorf("store error = %v, want %v", gotErr, tt.wantErr)
				}
			}
			if tt.want != nil {
				got := decodeAlert(t, rr.Body.String())
				if diff := cmp.Diff(*tt.want, got); diff != "" {
					t.Errorf("rearmed alert mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestGetEmail covers the profile-email read: unset returns {"email":null};
// a previously-set email reads back.
func TestGetEmail(t *testing.T) {
	tests := []struct {
		name      string
		seedEmail string // "" = no profile set
		wantBody  string
	}{
		{name: "unset returns null", seedEmail: "", wantBody: `{"email":null}`},
		{name: "set returns the email", seedEmail: "u@example.com", wantBody: `{"email":"u@example.com"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStore()
			if tt.seedEmail != "" {
				f.emails["key1"] = tt.seedEmail
			}
			rr := do(newTestRouter(f), "GET", "/email", "key1", "")
			if rr.Code != 200 {
				t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
			}
			if got := strings.TrimSpace(rr.Body.String()); got != tt.wantBody {
				t.Errorf("body = %s, want %s", got, tt.wantBody)
			}
		})
	}
}

// TestPutEmail covers validation and persistence: a valid address is stored and
// echoed; a malformed one is rejected 400 and nothing is written.
func TestPutEmail(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantStored string // "" = expect nothing stored
	}{
		{name: "valid address stored", body: `{"email":"new@example.com"}`, wantStatus: 200, wantStored: "new@example.com"},
		{name: "missing address rejected", body: `{"email":""}`, wantStatus: 400},
		{name: "malformed address rejected", body: `{"email":"not-an-email"}`, wantStatus: 400},
		{name: "name-form address rejected", body: `{"email":"Me <me@example.com>"}`, wantStatus: 400},
		{name: "malformed json rejected", body: `{nope`, wantStatus: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStore()
			rr := do(newTestRouter(f), "PUT", "/email", "key1", tt.body)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantStored == "" {
				if _, ok := f.emails["key1"]; ok {
					t.Errorf("email stored on failure: %q", f.emails["key1"])
				}
				return
			}
			if f.emails["key1"] != tt.wantStored {
				t.Errorf("stored = %q, want %q", f.emails["key1"], tt.wantStored)
			}
		})
	}
}

// decodeAlert unmarshals a single alert JSON body for whole-value comparisons.
func decodeAlert(t *testing.T, body string) alertResponse {
	t.Helper()
	var got alertResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("bad alert JSON %q: %v", body, err)
	}
	return got
}
