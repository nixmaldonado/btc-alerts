package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/awslabs/aws-lambda-go-api-proxy/gorillamux"
	"github.com/google/uuid"

	"github.com/nixmaldonado/btc-alerts/internal/api"
	"github.com/nixmaldonado/btc-alerts/internal/store"
)

func main() {
	tableName := os.Getenv("ALERTS_TABLE")
	if tableName == "" {
		log.Fatal("ALERTS_TABLE environment variable is required")
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	client := dynamodb.NewFromConfig(cfg)
	s := store.New(client, tableName)

	// Real store satisfies the narrow api.Store interface; inject real id + clock.
	h := api.New(s, time.Now, uuid.NewString)

	// The gorillamux adapter translates each API-Gateway (REST/v1) proxy event into
	// an *http.Request, routes it through the mux router, and converts the response
	// back. It also stashes the gateway request context so the handler's auth
	// middleware can read the validated APIKeyID (the tenant).
	adapter := gorillamux.New(h.Router())
	lambda.Start(func(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		rsp, err := adapter.ProxyWithContext(ctx, *core.NewSwitchableAPIGatewayRequestV1(&req))
		if err != nil {
			return events.APIGatewayProxyResponse{}, err
		}
		return *rsp.Version1(), nil
	})
}
