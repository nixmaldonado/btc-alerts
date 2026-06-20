// internal/price/coingecko_test.go
package price

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCurrentBTCUSD(t *testing.T) {
	tests := []struct {
		name      string
		status    int    // response status (0 → 200)
		body      string // response body
		wantErr   bool   // true → CurrentBTCUSD must return a non-nil error
		wantPrice float64
	}{
		{
			name:      "happy path",
			body:      `{"bitcoin":{"usd":70123.45}}`,
			wantPrice: 70123.45,
		},
		{
			name:    "non-200 is error",
			status:  http.StatusInternalServerError,
			wantErr: true,
		},
		{
			name:    "malformed JSON is error",
			body:    `{"bitcoin":`,
			wantErr: true,
		},
		{
			name:    "missing bitcoin.usd is error",
			body:    `{"ethereum":{"usd":3000}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/simple/price" {
					t.Errorf("path = %q, want /simple/price", r.URL.Path)
				}
				q := r.URL.Query()
				if q.Get("ids") != "bitcoin" || q.Get("vs_currencies") != "usd" {
					t.Errorf("query = %v, want ids=bitcoin&vs_currencies=usd", q)
				}
				if tt.status != 0 {
					w.WriteHeader(tt.status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := NewClient(srv.Client(), srv.URL)
			got, err := c.CurrentBTCUSD(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (price %v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Single scalar: a direct comparison is clearer than go-cmp here.
			if got != tt.wantPrice {
				t.Errorf("price = %v, want %v", got, tt.wantPrice)
			}
		})
	}
}
