package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"

	"github.com/nixmaldonado/btc-alerts/internal/notifier"
	"github.com/nixmaldonado/btc-alerts/internal/store"
)

func main() {
	sender := os.Getenv("SENDER_EMAIL")
	if sender == "" {
		log.Fatal("notifier: SENDER_EMAIL must be set")
	}
	tableName := os.Getenv("ALERTS_TABLE")
	if tableName == "" {
		log.Fatal("notifier: ALERTS_TABLE must be set")
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("notifier: load AWS config: %v", err)
	}

	sesClient := sesv2.NewFromConfig(cfg)
	ddb := dynamodb.NewFromConfig(cfg)
	handler := notifier.Handler{
		Sender:   notifier.NewSESSender(sesClient, sender),
		Resolver: store.New(ddb, tableName), // resolves the recipient from the owner profile
	}

	lambda.Start(handler.Handle)
}
