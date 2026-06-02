# BTC Alerts

## About

"btc-alerts" is a notification service to get alerts on Bitcoin price.

Subscriptions are done through webhooks.

2 modes of alerts available:
- Rolling 24 hours % change threshold
- Price target reached (fired once after armed)

Price target alert is fired once to avoid spamming subscriber if threshold is crossed multiple times.

Price target alerts can be re-armed manually without having to set all the information of the alert again.

## Pre-Requisites

[TBD]

## Data Flow Diagram

[TBD]

## Example

[TBD]

## Webhook Payload Schema

[TBD]

## Deployment

This service can be deployed using Terraform.

Having the infrastructure defined as code enables reproducibility and auditability (through version control).

Deploy using:

```
terraform init
terraform plan
terraform apply
```

## Cost Consideration 

The design of this service had the minimum cost possible in mind so it can service as many requests as possible before it starts incurring costs.

Lambda, SNS, SQS, CloudWatch (logs, metrics, and alarms), and DynamoDB run within AWS's always-free tier. DynamoDB uses provisioned capacity at 25 RCU and 25 WCU with auto-scaling disabled, which is the always-free ceiling and gives a hard $0 storage-cost floor. API Gateway is covered by the 12-month introductory free tier; after that, expected cost at demo scale is a few dollars per month.

### DoW attacks 

The service is protected against Denial-of-Wallet attacks through three layers: 
- API Gateway usage plans with per-key quotas
- API Gateway request throttling
- Lambda reserved concurrency. 
 
This caps the realistic cost impact of API abuse at a few dollars even under sustained attack.

## Current Status

Initial phase of development, most of the project hasn't been implemented yet.