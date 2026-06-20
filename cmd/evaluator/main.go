// cmd/evaluator/main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/nixmaldonado/btc-alerts/internal/evaluator"
	"github.com/nixmaldonado/btc-alerts/internal/price"
	"github.com/nixmaldonado/btc-alerts/internal/store"
)

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("evaluator: load aws config: %v", err)
	}

	tableName := os.Getenv("ALERTS_TABLE")
	if tableName == "" {
		log.Fatal("evaluator: ALERTS_TABLE env var is required")
	}

	ddb := dynamodb.NewFromConfig(cfg)
	st := store.New(ddb, tableName)

	// COINGECKO_BASE_URL is optional; price.NewClient falls back to the public API.
	baseURL := os.Getenv("COINGECKO_BASE_URL")
	if baseURL == "" {
		baseURL = price.DefaultBaseURL
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	prices := price.NewClient(httpClient, baseURL)

	// handler is the EventBridge target. Ticks stay effectively single-flight via
	// the 30s timeout (under the 60s interval) + no async retries (Terraform) and
	// the conditional ARMED->FIRED write in FireAlert, so crossing detection stays
	// ordered without a queue or reserved concurrency.
	handler := func(ctx context.Context) error {
		return evaluator.Run(ctx, st, prices, time.Now().UTC())
	}

	lambda.Start(handler)
}
