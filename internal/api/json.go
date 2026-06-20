package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/nixmaldonado/btc-alerts/internal/alert"
)

// createAlertRequest is the POST /alerts body. Exactly one of TargetPrice / Pct
// must be set; Email is always required. Pointers distinguish "absent" from zero.
type createAlertRequest struct {
	TargetPrice *float64 `json:"targetPrice,omitempty"`
	Pct         *float64 `json:"pct,omitempty"`
	Email       string   `json:"email"`
}

// alertResponse is the JSON shape returned for a single alert.
type alertResponse struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	Direction      string     `json:"direction"`
	TargetPrice    float64    `json:"targetPrice"`
	Email          string     `json:"email"`
	ReferencePrice float64    `json:"referencePrice"`
	Pct            *float64   `json:"pct,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	FiredAt        *time.Time `json:"firedAt,omitempty"`
}

func newAlertResponse(a alert.Alert) alertResponse {
	return alertResponse{
		ID:             a.ID,
		Status:         string(a.Status),
		Direction:      string(a.Direction),
		TargetPrice:    a.TargetPrice,
		Email:          a.Email,
		ReferencePrice: a.ReferencePrice,
		Pct:            a.Pct,
		CreatedAt:      a.CreatedAt,
		FiredAt:        a.FiredAt,
	}
}

// errorBody is the JSON error envelope.
type errorBody struct {
	Error string `json:"error"`
}

// writeJSON marshals v and writes it as a JSON response. If marshalling fails it
// degrades to a 500 with a plain message (no panics escape the handler).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	b, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to encode response"}`))
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeError writes a JSON {"error": msg} body with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}
