// internal/price/coingecko.go
package price

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// DefaultBaseURL is CoinGecko's public v3 API root.
const DefaultBaseURL = "https://api.coingecko.com/api/v3"

// Client fetches the current BTC/USD spot price from CoinGecko.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient builds a price client with an injected HTTP client and base URL,
// so tests can point it at an httptest server and inject timeouts.
func NewClient(httpClient *http.Client, baseURL string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{httpClient: httpClient, baseURL: baseURL}
}

// simplePriceResponse models {"bitcoin":{"usd":<number>}}.
// A pointer on the inner field lets us detect a missing usd value.
type simplePriceResponse struct {
	Bitcoin struct {
		USD *float64 `json:"usd"`
	} `json:"bitcoin"`
}

// CurrentBTCUSD returns the latest BTC price in USD.
// Non-200 responses, malformed bodies, and a missing bitcoin.usd field are errors.
func (c *Client) CurrentBTCUSD(ctx context.Context) (float64, error) {
	url := c.baseURL + "/simple/price?ids=bitcoin&vs_currencies=usd"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("price: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("price: do request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("price: unexpected status %d", resp.StatusCode)
	}

	var parsed simplePriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("price: decode body: %w", err)
	}
	if parsed.Bitcoin.USD == nil {
		return 0, fmt.Errorf("price: response missing bitcoin.usd field")
	}
	return *parsed.Bitcoin.USD, nil
}
