package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"time"

	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/gorilla/mux"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
	"github.com/nixmaldonado/btc-alerts/internal/store"
)

// Store is the narrow persistence surface the handler depends on — a subset of
// *store.Store. Keeping it local makes the handler unit-testable with an
// in-memory fake (no AWS/DynamoDB) and documents exactly what the API uses.
type Store interface {
	PutAlert(ctx context.Context, a alert.Alert) error
	ListAlerts(ctx context.Context, ownerID string) ([]alert.Alert, error)
	GetAlert(ctx context.Context, ownerID, id string) (alert.Alert, error)
	DeleteAlert(ctx context.Context, ownerID, id string) error
	RearmAlert(ctx context.Context, ownerID, id string) (alert.Alert, error)
	GetLastPrice(ctx context.Context) (price float64, ok bool, err error)
	GetOwnerEmail(ctx context.Context, ownerID string) (email string, ok bool, err error)
	PutOwnerEmail(ctx context.Context, ownerID, email string) error
}

// Handler holds the dependencies for the Price Alert API. clock and id are
// injected so tests are deterministic (no real time / UUIDs).
type Handler struct {
	store Store
	clock func() time.Time
	id    func() string
}

// New builds a Handler from a Store and the time/id generators.
func New(s Store, clock func() time.Time, id func() string) *Handler {
	return &Handler{store: s, clock: clock, id: id}
}

// ctxKey is a private context-key type so owner values can't collide with keys
// set by other packages.
type ctxKey int

const ownerKey ctxKey = iota

// withOwner returns a context carrying the resolved tenant (API-key) id.
func withOwner(ctx context.Context, ownerID string) context.Context {
	return context.WithValue(ctx, ownerKey, ownerID)
}

// ownerFromContext reads the tenant id placed on the request by requireOwner.
func ownerFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ownerKey).(string)
	return id
}

// Router builds the gorilla/mux router with auth middleware and the four routes.
// It is exported so cmd/api can hand it to the API-Gateway proxy adapter and so
// tests can drive it directly with net/http.
func (h *Handler) Router() *mux.Router {
	r := mux.NewRouter()
	r.Use(h.requireOwner)

	r.HandleFunc("/alerts", h.createAlert).Methods(http.MethodPost)
	r.HandleFunc("/alerts", h.listAlerts).Methods(http.MethodGet)
	r.HandleFunc("/alerts/{id}/rearm", h.rearmAlert).Methods(http.MethodPost)
	r.HandleFunc("/alerts/{id}", h.deleteAlert).Methods(http.MethodDelete)
	r.HandleFunc("/email", h.getEmail).Methods(http.MethodGet)
	r.HandleFunc("/email", h.putEmail).Methods(http.MethodPut)

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	r.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	})
	return r
}

// requireOwner resolves the tenant for every matched route. The API key is the
// tenant: in the real Lambda the owner is the validated APIKeyID carried in the
// API-Gateway request context (populated by the proxy adapter); tests inject it
// directly via withOwner. An empty owner is rejected with 401. mux only runs
// Use middleware on matched routes, so unknown paths fall through to 404 here.
func (h *Handler) requireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner := ownerFromContext(r.Context())
		if owner == "" {
			if gw, ok := core.GetAPIGatewayContextFromContext(r.Context()); ok {
				owner = gw.Identity.APIKeyID
			}
		}
		if owner == "" {
			writeError(w, http.StatusUnauthorized, "missing API key")
			return
		}
		next.ServeHTTP(w, r.WithContext(withOwner(r.Context(), owner)))
	})
}

func (h *Handler) createAlert(w http.ResponseWriter, r *http.Request) {
	ownerID := ownerFromContext(r.Context())

	var body createAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Exactly one of targetPrice / pct must be supplied.
	if (body.TargetPrice == nil) == (body.Pct == nil) {
		writeError(w, http.StatusBadRequest, "provide exactly one of targetPrice or pct")
		return
	}

	// The recipient is the tenant's profile email; it must be set before any alert
	// can be created, since the notifier resolves delivery from it at fire time.
	email, ok, err := h.store.GetOwnerEmail(r.Context(), ownerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read profile email")
		return
	}
	if !ok || email == "" {
		writeError(w, http.StatusConflict, "set your notification email first")
		return
	}

	price, ok, err := h.store.GetLastPrice(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read current price")
		return
	}
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "price not yet available")
		return
	}

	targetPrice := price
	if body.TargetPrice != nil {
		targetPrice = *body.TargetPrice
	} else {
		targetPrice = alert.TargetFromPct(price, *body.Pct)
	}

	a, err := alert.NewAlert(ownerID, h.id(), targetPrice, price, body.Pct, h.clock())
	if err != nil {
		// All NewAlert errors are caller input problems → 400.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.PutAlert(r.Context(), a); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store alert")
		return
	}
	writeJSON(w, http.StatusCreated, newAlertResponse(a))
}

func (h *Handler) listAlerts(w http.ResponseWriter, r *http.Request) {
	ownerID := ownerFromContext(r.Context())

	alerts, err := h.store.ListAlerts(r.Context(), ownerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list alerts")
		return
	}
	// Always emit a JSON array, never null, even when there are no alerts.
	out := make([]alertResponse, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, newAlertResponse(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) rearmAlert(w http.ResponseWriter, r *http.Request) {
	ownerID := ownerFromContext(r.Context())
	id := mux.Vars(r)["id"]

	a, err := h.store.RearmAlert(r.Context(), ownerID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to rearm alert")
		return
	}
	writeJSON(w, http.StatusOK, newAlertResponse(a))
}

func (h *Handler) deleteAlert(w http.ResponseWriter, r *http.Request) {
	ownerID := ownerFromContext(r.Context())
	id := mux.Vars(r)["id"]

	if err := h.store.DeleteAlert(r.Context(), ownerID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete alert")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getEmail returns the tenant's notification email, or {"email": null} when unset.
func (h *Handler) getEmail(w http.ResponseWriter, r *http.Request) {
	ownerID := ownerFromContext(r.Context())

	email, ok, err := h.store.GetOwnerEmail(r.Context(), ownerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read profile email")
		return
	}
	out := emailResponse{}
	if ok {
		out.Email = &email
	}
	writeJSON(w, http.StatusOK, out)
}

// putEmail sets (or replaces) the tenant's notification email after validating it.
// Updating it changes delivery for every one of the tenant's alerts, since the
// notifier resolves the recipient from this profile at fire time.
func (h *Handler) putEmail(w http.ResponseWriter, r *http.Request) {
	ownerID := ownerFromContext(r.Context())

	var body emailRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// ParseAddress also accepts "Name <addr>"; require the bare address to match so
	// we store exactly what SES will send to.
	addr, err := mail.ParseAddress(body.Email)
	if err != nil || addr.Address != body.Email {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}

	if err := h.store.PutOwnerEmail(r.Context(), ownerID, body.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save email")
		return
	}
	writeJSON(w, http.StatusOK, emailResponse{Email: &body.Email})
}
