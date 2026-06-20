# BTC Alerts — P0 Design

**Date:** 2026-06-20
**Status:** Approved (design phase)
**Scope:** P0 — price-target-reached alerts delivered by email.

## 1. Purpose & Goals

`btc-alerts` is a serverless notification service: a user registers an alert for a
Bitcoin price target, and receives an email when BTC crosses that target. This is a
**portfolio showcase** — optimize for legibility and demoability, and keep running cost
at or near $0 on AWS's always-free tier.

**P0 (this spec)**
- Price-target-reached alerts (absolute target, or percentage resolved to absolute at creation).
- An alert fires **once**, then disarms; it can be re-armed manually.
- Delivery by email via Amazon SES (sandbox mode).
- Access gated by API keys minted on demand (the DoW / external-abuse guard).

**Out of scope (v2)**
- Webhook delivery channel.
- Rolling 24-hour % change alerts (needs a price-history store + separate evaluation path).

## 2. Tenancy & Identity

- **The API key is the tenant.** API Gateway validates the key and passes its key ID to
  the API Lambda as `apiKeyId`. Alerts are owned by the key that created them, so each
  tester only sees and manages their own alerts. No separate user table.
- **Email is a delivery field**, not an identity. Because SES runs in sandbox mode, a
  recipient address must be verified before it can receive mail.
- **Onboarding (manual, on-demand):** to give a prospect access, mint an API key + bind it
  to the usage plan, and verify their email address in SES (one-click via AWS's
  verification email). This is a deliberate manual step — keys are handed out by hand.

## 3. Architecture

```
EventBridge (rate: 1 min)
        │
        ▼
  Evaluator Lambda  ──(GetItem/PutItem STATE#PRICE)──┐
  (reserved concurrency = 1)                          │
        │  CoinGecko price fetch                      │
        │  Query GSI1 for crossed armed alerts        ▼
        │  Conditional update ARMED→FIRED        ┌──────────────┐
        └───────────────────────────────────────▶│  DynamoDB    │
                                                  │  table:alerts│
   API Gateway (API-key auth + usage plan)        │  + GSI1      │
        │                                         │  + Stream    │
        ▼                                         └──────┬───────┘
  Price Alert API Lambda ─────────────────────────────  │
  (create / list / rearm / delete)                      │ Stream (FilterCriteria:
                                                         │  PK begins_with OWNER#)
                                                         ▼
                                                  Notifier Lambda ──▶ SES email
                                                         │
                                                         ▼ (on failure)
                                                  SQS dead-letter queue
```

## 4. Data Model

Single DynamoDB table `alerts` (single-table design). Two item types.

### 4.1 Alert item

| Attribute | Type | Notes |
|---|---|---|
| `PK` | S | `OWNER#<apiKeyId>` |
| `SK` | S | `ALERT#<alertId>` (`alertId` = UUID) |
| `status` | S | `ARMED` \| `FIRED` |
| `direction` | S | `ABOVE` \| `BELOW` — derived at creation (target vs. reference price) |
| `targetPrice` | N | absolute USD target |
| `email` | S | subscriber email (must be SES-verified) |
| `referencePrice` | N | BTC price at creation time |
| `pct` | N | original percentage, if the alert was created as a % target (optional) |
| `createdAt` | S | ISO-8601 |
| `firedAt` | S | ISO-8601, set when fired (optional) |
| `gsi_pk` | S | `ARMED#ABOVE` \| `ARMED#BELOW` — **present only while armed (sparse)** |
| `gsi_sk` | S | zero-padded `targetPrice` (lexicographic = numeric order) |

**Direction** is fixed at creation: if `targetPrice > referencePrice` the alert is `ABOVE`
(fires when price rises through the target); if `targetPrice < referencePrice` it is
`BELOW` (fires when price falls through the target).

### 4.2 Price-state singleton

| Attribute | Type | Notes |
|---|---|---|
| `PK` | S | `STATE#PRICE` |
| `SK` | S | `STATE#PRICE` |
| `lastPrice` | N | last observed BTC price |
| `lastSeenAt` | S | ISO-8601 |

Kept in the same table for single-table simplicity. It is **excluded from the Notifier**
by a stream `FilterCriteria` (see §6), so its once-a-minute writes never wake the Notifier.

### 4.3 GSI1 — `status-target-index`

- **Partition key:** `gsi_pk` (`ARMED#ABOVE` / `ARMED#BELOW`)
- **Sort key:** `gsi_sk` (zero-padded target price)
- **Sparse:** only armed alerts carry `gsi_pk`/`gsi_sk`, so firing or disarming removes the
  alert from the index automatically; re-arming restores it. The Evaluator's range Query
  therefore only ever sees armed alerts.
- **Projection:** `KEYS_ONLY` + `email` + `direction` (enough to locate and conditionally
  update the base item; the Notifier gets full data from the stream).

### 4.4 Access patterns

| # | Pattern | Query |
|---|---|---|
| A | List a tester's alerts | `PK = OWNER#<apiKeyId>`, `SK begins_with ALERT#` |
| B | Get / update / delete one alert | `PK = OWNER#<apiKeyId>`, `SK = ALERT#<alertId>` |
| C | Find armed alerts crossed this tick | GSI1: `gsi_pk = ARMED#<dir>` and `gsi_sk BETWEEN ...` |
| D | Read / write last price | `PK = SK = STATE#PRICE` |

## 5. Evaluator Lambda

- **Trigger:** EventBridge schedule, `rate(1 minute)`.
- **Concurrency:** reserved concurrency `1` — ticks processed serially, so crossing
  detection stays ordered without a queue.
- **Per-tick logic:**
  1. Read `STATE#PRICE` → previous price `P` (may be absent on first run).
  2. Fetch current price `C` from CoinGecko.
  3. Write `STATE#PRICE` = `C`, `lastSeenAt = now`.
  4. If `P` is absent (first tick) → seed only, fire nothing.
  5. If `C > P` (rose): Query GSI1 `gsi_pk = ARMED#ABOVE`, `gsi_sk` in `(P, C]`.
     If `C < P` (fell): Query GSI1 `gsi_pk = ARMED#BELOW`, `gsi_sk` in `[C, P)`.
     If `C == P`: nothing.
  6. For each hit, conditional update on the base item:
     - condition: `status = ARMED`
     - set `status = FIRED`, `firedAt = now`
     - **remove `gsi_pk`, `gsi_sk`** (drops it from the sparse index)
     The `status = ARMED` condition makes firing idempotent — a retry can't double-fire.

## 6. Notifier Lambda

- **Trigger:** DynamoDB Stream on the `alerts` table.
- **Stream `FilterCriteria`:** only deliver records where the item is an alert — i.e.
  `dynamodb.Keys.PK.S` **begins with `OWNER#`**. This filters out every `STATE#PRICE`
  write at the event source, so price-register churn never invokes the Notifier.
- **Logic:** for records where `status` transitioned to `FIRED`, send an SES email to the
  item's `email` with the alert details (target, direction, current price, fired-at).
- **DLQ:** an SQS dead-letter queue captures records that fail after retries.
- **Why stream-decoupled:** delivery is driven off the committed write, so a notification
  can't be lost or duplicated by a partial write in the Evaluator.

## 7. Price Alert API

API Gateway (REST) + Lambda. **Auth:** API key required on every route; a usage plan
enforces per-key quota + throttle (the DoW guard). The validated key ID is the `apiKeyId`
owner.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/alerts` | Create. Body: `{ targetPrice \| pct, email }`. Resolves `pct`→absolute (`target = referencePrice * (1 + pct)`), derives `direction` from current price, sets `status = ARMED`, writes `gsi_pk/sk`. |
| `GET` | `/alerts` | List the caller's alerts (access pattern A). |
| `POST` | `/alerts/{id}/rearm` | Set `status = ARMED`, restore `gsi_pk/sk`, clear `firedAt`. |
| `DELETE` | `/alerts/{id}` | Delete the alert. |

All mutating/reading routes scope to `PK = OWNER#<apiKeyId>`, so a key can only touch its
own alerts.

## 8. Cost Posture

- **DynamoDB:** **provisioned** capacity to honor the hard $0 floor (on-demand has no free
  request tier). The always-free ceiling is **25 RCU / 25 WCU account-wide**, and a
  provisioned GSI needs its own capacity, so capacity is **split across the base table and
  GSI1** to stay within 25/25 total each (demo load is a tick/minute plus a handful of
  testers, so small allocations suffice). Auto-scaling disabled.
- **Lambda, DynamoDB Streams, SQS, EventBridge, CloudWatch:** always-free tier.
- **SES:** sandbox mode (free allotment covers 3,000 messages/month).
- **API Gateway:** 12-month introductory free tier; a few $/month after at demo scale.

### DoW protection (three layers)
- API Gateway usage plans with per-key quotas.
- API Gateway request throttling.
- Lambda reserved concurrency.

## 9. Infrastructure (Terraform)

Fix the existing stub (`billing_mode` must be `PROVISIONED` with read/write capacity, not
the invalid `"ON-DEMAND"`; the table currently has no keys). Provisions:

- DynamoDB table `alerts` (PK/SK, provisioned capacity, Streams enabled) + GSI1.
- Three Lambdas: Evaluator, Notifier, Price Alert API (Go 1.23).
- EventBridge rule (`rate(1 minute)`) → Evaluator; Evaluator reserved concurrency `1`.
- DynamoDB Stream event-source mapping → Notifier, with `FilterCriteria` (PK begins_with
  `OWNER#`) and SQS DLQ.
- API Gateway REST API + Lambda integration + API keys + usage plan (quota/throttle).
- SES identity / verified-sender config.
- CloudWatch logs, metrics, alarms.

## 10. Open Questions / Notes

- **CoinGecko rate limits:** the free endpoint tolerates ~1 call/min comfortably; no API
  key needed at this cadence. Revisit if cadence increases.
- **Exact crossing at equality / re-arm at current price:** an alert whose target equals
  the current price on the seeding tick won't fire until a subsequent crossing; acceptable
  for P0.
- **GSI hot partition:** `ARMED#ABOVE` / `ARMED#BELOW` are two partitions shared by all
  armed alerts — fine at demo scale; at real scale you'd shard the partition key. Noted as
  a known scaling tradeoff.
