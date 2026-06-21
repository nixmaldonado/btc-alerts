# BTC Alerts

## About

"btc-alerts" is a notification service to get alerts on Bitcoin price.

Notifications are delivered by email (Amazon SES). Webhook delivery is planned as a v2 channel.

Two alert modes:
- **Price target reached** (P0) — fires once when BTC crosses an absolute target price. Percentage targets are supported by computing the absolute target at creation time (`target = reference_price * (1 + pct)`), so they evaluate on the same path.
- **Rolling 24-hour % change threshold** (v2, not yet implemented) — needs a price-history store and a separate evaluation path.

A price-target alert fires once and then disarms, to avoid spamming the subscriber if the threshold is crossed multiple times. It can be re-armed manually without re-entering the alert's configuration.

## Pre-Requisites

[TBD]

## Data Flow Diagram

<p align="center">
  <img src="docs/architecture.svg?v=3" alt="BTC Alerts data flow: an EventBridge-scheduled evaluator Lambda fetches BTC prices from CoinGecko and writes fired alerts to DynamoDB; a DynamoDB Streams trigger drives a Notifier Lambda that sends email via SES; users configure alerts through API Gateway." width="900">
</p>

The evaluator Lambda runs on an EventBridge schedule with reserved concurrency `1`, so price ticks are processed serially. This keeps the "previous price → current price" crossing detection ordered without needing a queue. On each tick it pulls the latest BTC price from CoinGecko and fires any armed alerts whose target the price just crossed. Firing is a single conditional write to DynamoDB (`armed → fired`); a DynamoDB Streams trigger then drives the Notifier Lambda, which delivers the alert email through SES. Decoupling delivery from evaluation via the stream means a notification can never be lost or duplicated by a partial write. Users manage their alerts through API Gateway in front of the Price Alert API Lambda.

## Try the Demo

A live instance is running at **https://btc-alerts.nixmaldonado.com** — a small browser dashboard
where you can create and manage BTC price alerts without touching `curl`.

In this service **the API key *is* the tenant**: each key only ever sees its own alerts, and rate
limits apply per key. There's no account/signup — your key is your credential.

1. **Get a key.** Keys are issued out-of-band (no self-signup, to keep the demo free and abuse-resistant).
   On the login page, use the **"Need a key? Message me →"** Telegram link to request one. You'll
   receive a magic link of the form `https://btc-alerts.nixmaldonado.com/#k=<your-key>`.
2. **Log in.** Tap the magic link. The page reads the key from the URL fragment, saves it to your
   browser's `localStorage`, and scrubs it from the address bar — so the key never hits a server or
   a link preview. You land on **Your alerts**. Re-clicking the link always works; **Log out** clears
   the stored key.
3. **Create an alert.** Enter an email and **exactly one** of:
   - **Target price (USD)** — fires when BTC crosses that absolute price.
   - **% move** — the absolute target is computed once at creation from the current price
     (e.g. `+5%` → `current × 1.05`), then it evaluates on the same path.

   The new alert shows up in the table as **ARMED**.
4. **Watch it fire.** A scheduled evaluator pulls the latest BTC price and, when your target is
   crossed, flips the row **ARMED → FIRED** and sends the alert email. An alert fires **once** and
   then disarms, so you're not spammed if the price wobbles across the threshold repeatedly.

   > **Email caveat:** SES runs in **sandbox mode** for this demo, so the alert email only delivers
   > to addresses that have been verified with SES. You can still exercise the entire dashboard
   > (create / list / delete) with any email — only the outbound email is gated.
5. **Manage.** **Delete** removes an alert. A **FIRED** alert can be re-armed (a **Rearm** button
   appears on fired rows) to reuse it without re-entering its configuration.

### What you're exercising

Every dashboard action travels the full stack — the browser calls a thin Cloudflare Pages proxy
(`/api/*`, which avoids CORS and attaches your key as `x-api-key`), which forwards to API Gateway →
the Price Alert API Lambda → DynamoDB. Firing and delivery run on the separate evaluator/notifier
path described above.

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

Lambda, DynamoDB (including DynamoDB Streams), SQS (the Notifier's dead-letter queue), EventBridge (scheduler), and CloudWatch (logs, metrics, and alarms) run within AWS's always-free tier. DynamoDB uses provisioned capacity at 25 RCU and 25 WCU with auto-scaling disabled, which is the always-free ceiling and gives a hard $0 storage-cost floor. Reading DynamoDB Streams from a Lambda trigger incurs no additional charge. SES runs in sandbox mode for the demo (its free allotment covers 3,000 messages/month). API Gateway is covered by the 12-month introductory free tier; after that, expected cost at demo scale is a few dollars per month.

### DoW attacks 

The service is protected against Denial-of-Wallet attacks through three layers: 
- API Gateway usage plans with per-key quotas
- API Gateway request throttling
- Lambda reserved concurrency. 
 
This caps the realistic cost impact of API abuse at a few dollars even under sustained attack.

## Current Status

Initial phase of development, most of the project hasn't been implemented yet.