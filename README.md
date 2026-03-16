# openpay-smart-service

A multi-tenant **Go + gRPC** service that wraps the [OpenPay by BBVA](https://www.openpay.mx) REST API, exposing a strongly-typed, protocol-buffer-defined interface for payment orchestration, disbursement scheduling, balance aggregation, and real-time webhook relay.

```
Base URL (sandbox) : https://sandbox-api.openpay.mx/v1/{merchantId}
Base URL (prod)    : https://api.openpay.mx/v1/{merchantId}
Auth               : HTTP Basic — private API key as username, empty password
```

---

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Domain Model](#3-domain-model)
4. [gRPC Service Definitions](#4-grpc-service-definitions)
   - 4.1 [TenantService](#41-tenantservice)
   - 4.2 [MemberService](#42-memberservice)
   - 4.3 [PaymentService](#43-paymentservice)
   - 4.4 [BalanceService](#44-balanceservice)
   - 4.5 [WebhookService](#45-webhookservice)
5. [OpenPay API Mapping](#5-openpay-api-mapping)
6. [Webhook Event Catalogue](#6-webhook-event-catalogue)
7. [Data Storage Design](#7-data-storage-design)
8. [Cross-Cutting Concerns](#8-cross-cutting-concerns)
9. [Security](#9-security)
10. [Project Layout](#10-project-layout)
11. [Configuration](#11-configuration)
12. [Getting Started](#12-getting-started)
13. [Implementation Phases](#13-implementation-phases)
14. [Non-Functional Requirements](#14-non-functional-requirements)

---

## 1. Overview

`openpay-smart-service` acts as a **smart gateway layer** over OpenPay's REST API. Platform operators (called *tenants*) use the service to:

- Onboard end-users (*members*) and issue payment links tied to them
- Collect card and bank-transfer payments via OpenPay
- Query payment status and history with rich filtering
- Manage their OpenPay card/bank-account vault
- Receive funds on a configurable disbursement schedule (daily / weekly / monthly / custom cron)
- Monitor real-time events through registered webhooks, with guaranteed-delivery semantics on top of OpenPay's native notifications

The service never stores raw card data — all sensitive card handling is delegated to OpenPay, keeping PCI-DSS scope to a minimum.

---

## 2. Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                     openpay-smart-service                           │
│                                                                     │
│  ┌──────────────┐   gRPC / REST-GW   ┌───────────────────────────┐ │
│  │ Tenant API   │ ──────────────────► │  gRPC Server              │ │
│  │ Clients      │                    │  (TenantSvc, MemberSvc,   │ │
│  └──────────────┘                    │   PaymentSvc, BalanceSvc, │ │
│                                      │   WebhookSvc)             │ │
│  ┌──────────────┐   payment links    └──────────┬────────────────┘ │
│  │ Members      │ ◄──────────────────────────── │                  │
│  │ (end users)  │                               │                  │
│  └──────────────┘                    ┌──────────▼────────────────┐ │
│                                      │  Domain / Business Logic  │ │
│  ┌──────────────┐   webhooks         │  (payment engine,         │ │
│  │ Tenant       │ ◄──────────────── │   scheduler, relay)       │ │
│  │ Endpoints    │                    └──────────┬────────────────┘ │
│  └──────────────┘                               │                  │
└─────────────────────────────────────────────────┼────────────────── ┘
                                                  │ HTTPS / Basic Auth
                          ┌───────────────────────▼──────────────────┐
                          │        OpenPay by BBVA REST API           │
                          │  /customers  /charges  /payouts           │
                          │  /cards  /transfers  /fees  /plans        │
                          │  /subscriptions  /webhooks                │
                          └──────────────────────────────────────────┘
                                        │
                          ┌─────────────┴────────────┐
                          │  PostgreSQL  │   Redis    │
                          │  (state,     │  (cache,   │
                          │   audit log) │   rate lim)│
                          └─────────────┴────────────┘
                                        │
                          ┌─────────────▼────────────┐
                          │          Kafka            │
                          │  payment.events           │
                          │  webhook.outbound / dlq   │
                          │  disbursement.commands    │
                          └──────────────────────────┘
```

### Key design decisions

| Decision | Choice | Rationale |
|---|---|---|
| API protocol | gRPC + grpc-gateway | Strongly typed contracts; REST fallback for browser/mobile clients |
| Upstream payments | OpenPay BBVA REST | Regulatory-compliant payment processing in MX; SPEI + card support |
| Card vault | OpenPay token API | Eliminates raw PANs from our stack; reduces PCI-DSS scope |
| Event delivery | Kafka outbox + HTTP relay | At-least-once semantics with retry/DLQ on top of OpenPay webhooks |
| Multi-tenancy | tenant_id column + RLS | Isolation at both app and DB layers |
| Idempotency | Redis + DB constraint | Prevents double-charges on client retries |

---

## 3. Domain Model

```
Tenant ──< Member ──< PaymentLink ──< Payment
  │           │
  │           └──< Balance
  │
  ├──< WebhookSubscription ──< WebhookDelivery
  ├──< DisbursementSchedule
  └──< TenantCardConfig  (OpenPay card/bank-account token)
```

| Entity | Maps to OpenPay | Key Fields |
|---|---|---|
| `Tenant` | Merchant account | `id`, `merchant_id`, `api_key_hash`, `openpay_private_key` (encrypted), `created_at` |
| `Member` | Customer (`/customers`) | `id`, `tenant_id`, `openpay_customer_id`, `external_id`, `name`, `email`, `phone`, `kyc_status` |
| `PaymentLink` | — (service-layer concept) | `id`, `tenant_id`, `member_id`, `amount`, `currency`, `description`, `status`, `expires_at`, `token` |
| `Payment` | Charge (`/charges`) | `id`, `tenant_id`, `member_id`, `openpay_transaction_id`, `order_id`, `amount`, `currency`, `method`, `status`, `idempotency_key` |
| `Payout` | Payout (`/payouts`) | `id`, `tenant_id`, `openpay_transaction_id`, `amount`, `currency`, `status`, `scheduled_for` |
| `Balance` | Derived from charges/payouts | `tenant_id`, `member_id`, `available`, `pending`, `currency` |
| `DisbursementSchedule` | Triggers `/payouts` | `tenant_id`, `frequency`, `cron_expr`, `next_run_at`, `last_run_at` |
| `TenantCardConfig` | Card token (`/cards`) | `tenant_id`, `openpay_card_id`, `last_four`, `brand`, `holder_name` |
| `WebhookSubscription` | Webhook (`/webhooks`) | `id`, `tenant_id`, `url`, `secret`, `events[]`, `retry_policy` |
| `WebhookDelivery` | Outbound delivery log | `id`, `subscription_id`, `openpay_event_type`, `payload`, `status`, `attempts` |

---

## 4. gRPC Service Definitions

All services live under the package `openpay.v1`. Every RPC receives the tenant's API key via the `Authorization: Bearer <key>` gRPC metadata header, which the auth interceptor resolves to a `TenantContext` before the handler is invoked.

> Proto files live in `proto/openpay/v1/`. The REST gateway (`grpc-gateway`) exposes all RPCs as HTTP/JSON endpoints under `/v1/`.

---

### 4.1 TenantService

Manages tenant configuration, card/bank-account vault via OpenPay, and disbursement schedule.

```protobuf
service TenantService {
  rpc SetupTenantCardInfo       (SetupTenantCardInfoRequest)      returns (SetupTenantCardInfoResponse);
  rpc GetTenantCardInfo         (GetTenantCardInfoRequest)        returns (GetTenantCardInfoResponse);
  rpc DeleteTenantCardInfo      (DeleteTenantCardInfoRequest)     returns (DeleteTenantCardInfoResponse);
  rpc SetDisbursementFrequency  (SetDisbursementFrequencyRequest) returns (SetDisbursementFrequencyResponse);
  rpc GetDisbursementSchedule   (GetDisbursementScheduleRequest)  returns (GetDisbursementScheduleResponse);
  rpc TriggerManualDisbursement (TriggerManualDisbursementRequest)returns (TriggerManualDisbursementResponse); // [v2]
}
```

| RPC | OpenPay call | Notes |
|---|---|---|
| `SetupTenantCardInfo` | `POST /customers/{id}/cards` | Accepts an OpenPay card token (generated client-side via JS SDK); stores the returned `card_id` |
| `GetTenantCardInfo` | `GET /customers/{id}/cards/{cardId}` | Returns masked card data only (`last_four`, brand, holder) |
| `DeleteTenantCardInfo` | `DELETE /customers/{id}/cards/{cardId}` | Removes card from OpenPay vault and local record |
| `SetDisbursementFrequency` | — | Stores cron config; scheduler uses it to trigger `/payouts` |
| `GetDisbursementSchedule` | — | Returns next and last disbursement timestamps with config |
| `TriggerManualDisbursement` | `POST /payouts` | Immediately initiates a payout for accrued balance |

---

### 4.2 MemberService

Manages end-users (OpenPay Customers) and payment link lifecycle.

```protobuf
service MemberService {
  rpc CreateMember       (CreateMemberRequest)       returns (CreateMemberResponse);
  rpc GetMember          (GetMemberRequest)          returns (GetMemberResponse);
  rpc UpdateMember       (UpdateMemberRequest)       returns (UpdateMemberResponse);
  rpc ListMembers        (ListMembersRequest)        returns (ListMembersResponse);
  rpc CreatePaymentLink  (CreatePaymentLinkRequest)  returns (CreatePaymentLinkResponse);
  rpc GetPaymentLink     (GetPaymentLinkRequest)     returns (GetPaymentLinkResponse);
  rpc ListPaymentLinks   (ListPaymentLinksRequest)   returns (ListPaymentLinksResponse);
  rpc ExpirePaymentLink  (ExpirePaymentLinkRequest)  returns (ExpirePaymentLinkResponse);
}
```

| RPC | OpenPay call | Notes |
|---|---|---|
| `CreateMember` | `POST /customers` | Creates OpenPay customer; stores `openpay_customer_id` locally; idempotency key required |
| `GetMember` | `GET /customers/{id}` | Merges OpenPay response with local metadata |
| `UpdateMember` | `PUT /customers/{id}` | Syncs name, email, phone to OpenPay customer record |
| `ListMembers` | — | Local DB query; OpenPay has no customer search endpoint |
| `CreatePaymentLink` | — | Service-layer token; no direct OpenPay call until link is redeemed |
| `GetPaymentLink` | — | Resolves token → link metadata + status |
| `ListPaymentLinks` | — | Cursor-paginated query scoped to `tenant_id` + optional `member_id` |
| `ExpirePaymentLink` | — | Marks link as `expired` locally; prevents redemption |

---

### 4.3 PaymentService

Manages the charge lifecycle and provides streaming event updates.

```protobuf
service PaymentService {
  rpc GetPaymentStatus    (GetPaymentStatusRequest)   returns (GetPaymentStatusResponse);
  rpc ListPayments        (ListPaymentsRequest)       returns (ListPaymentsResponse);
  rpc ListTenantPayments  (ListTenantPaymentsRequest) returns (ListTenantPaymentsResponse);
  rpc RefundPayment       (RefundPaymentRequest)      returns (RefundPaymentResponse);       // [v2]
  rpc StreamPaymentEvents (StreamPaymentEventsRequest)returns (stream PaymentEvent);
}
```

| RPC | OpenPay call | Notes |
|---|---|---|
| `GetPaymentStatus` | `GET /charges/{transactionId}` | Returns OpenPay status + local metadata; Redis-cached for 30 s |
| `ListPayments` | `GET /charges` | Calls OpenPay with `offset`/`limit`; merges with local filter (member, status, date, amount range) |
| `ListTenantPayments` | `GET /charges` scoped by `creation_date` | Optimised merchant-level listing without per-customer scan |
| `RefundPayment` | `POST /charges/{id}/refund` | Full or partial refund; propagates back via webhook |
| `StreamPaymentEvents` | — | Server-side streaming fed by the Kafka `payment.events` topic |

**ListPaymentsRequest filters:**

```protobuf
message PaymentFilter {
  string   member_id      = 1;
  repeated PaymentStatus status = 2;
  string   currency       = 3;
  int64    amount_min     = 4;  // cents / minor units
  int64    amount_max     = 5;
  google.protobuf.Timestamp from = 6;
  google.protobuf.Timestamp to   = 7;
  PaymentMethod method    = 8;  // CARD | BANK_ACCOUNT | STORE
  string   order_id       = 9;
}
```

---

### 4.4 BalanceService

Aggregated balance views derived from OpenPay charge/payout data.

```protobuf
service BalanceService {
  rpc GetTenantBalance  (GetTenantBalanceRequest)  returns (GetTenantBalanceResponse);
  rpc GetMemberBalance  (GetMemberBalanceRequest)  returns (GetMemberBalanceResponse);
  rpc ListBalances      (ListBalancesRequest)      returns (ListBalancesResponse);
  rpc GetBalanceHistory (GetBalanceHistoryRequest) returns (GetBalanceHistoryResponse); // [v2]
}
```

| RPC | OpenPay call | Notes |
|---|---|---|
| `GetTenantBalance` | — | Aggregated from local `payments` + `payouts` tables; `available` = completed charges minus disbursed payouts; `pending` = in-progress charges |
| `GetMemberBalance` | — | Same logic scoped to a single `member_id` |
| `ListBalances` | — | Paginated snapshot per member under a tenant |
| `GetBalanceHistory` | — | Time-series with configurable `granularity` (hour/day/week) |

---

### 4.5 WebhookService

Manages tenant webhook subscriptions and delivery telemetry. This service sits on top of OpenPay's own webhook system: OpenPay delivers events to a single internal endpoint, which the service then fans out to each tenant's registered endpoints according to their subscription filters.

```protobuf
service WebhookService {
  rpc RegisterWebhook      (RegisterWebhookRequest)      returns (RegisterWebhookResponse);
  rpc UpdateWebhook        (UpdateWebhookRequest)        returns (UpdateWebhookResponse);
  rpc DeleteWebhook        (DeleteWebhookRequest)        returns (DeleteWebhookResponse);
  rpc ListWebhooks         (ListWebhooksRequest)         returns (ListWebhooksResponse);
  rpc GetWebhookDelivery   (GetWebhookDeliveryRequest)   returns (GetWebhookDeliveryResponse);
  rpc RetryWebhookDelivery (RetryWebhookDeliveryRequest) returns (RetryWebhookDeliveryResponse);
  rpc RotateWebhookSecret  (RotateWebhookSecretRequest)  returns (RotateWebhookSecretResponse);
}
```

---

## 5. OpenPay API Mapping

Full endpoint-to-RPC cross-reference:

| OpenPay Endpoint | Method | Our RPC |
|---|---|---|
| `/customers` | POST | `MemberService.CreateMember` |
| `/customers/{id}` | GET | `MemberService.GetMember` |
| `/customers/{id}` | PUT | `MemberService.UpdateMember` |
| `/customers/{id}/cards` | POST | `TenantService.SetupTenantCardInfo` |
| `/customers/{id}/cards/{cardId}` | GET | `TenantService.GetTenantCardInfo` |
| `/customers/{id}/cards/{cardId}` | DELETE | `TenantService.DeleteTenantCardInfo` |
| `/charges` | GET | `PaymentService.ListTenantPayments` |
| `/customers/{id}/charges` | GET | `PaymentService.ListPayments` (member-scoped) |
| `/charges/{transactionId}` | GET | `PaymentService.GetPaymentStatus` |
| `/charges/{transactionId}/refund` | POST | `PaymentService.RefundPayment` [v2] |
| `/payouts` | POST | `TenantService.TriggerManualDisbursement` / Scheduler |
| `/payouts/{transactionId}` | GET | Internal payout status polling |
| `/fees` | GET | Internal fee reconciliation (audit log) |
| `/transfers` | POST / GET | Internal member-to-member transfers [v2] |
| `/plans` | POST / GET | Subscription plan management [v2] |
| `/customers/{id}/subscriptions` | POST / GET | Recurring charge setup [v2] |
| `/webhooks` | POST | Internal ingress endpoint — fans out to tenant subscriptions |

> **Authentication:** Every outbound call uses the tenant's encrypted `openpay_private_key` retrieved from the config store. The key is passed as the Basic Auth username with an empty password, over TLS 1.3.

---

## 6. Webhook Event Catalogue

OpenPay delivers the following event types to the service's internal ingress endpoint (`POST /internal/openpay/events`). The dispatcher then routes each event to matching tenant webhook subscriptions.

| OpenPay Event Type | Description | We Emit To Tenant |
|---|---|---|
| `verification` | Endpoint verification ping | ✓ (pass-through) |
| `charge.created` | Charge record created (pending) | ✓ |
| `charge.succeeded` | Card / SPEI charge completed successfully | ✓ |
| `charge.failed` | Charge declined or timed out | ✓ |
| `charge.cancelled` | Charge voided before capture | ✓ |
| `charge.refunded` | Full or partial refund processed | ✓ |
| `subscription.charge.failed` | Recurring subscription charge failed | ✓ [v2] |
| `payout.created` | Payout record created | ✓ |
| `payout.succeeded` | Funds disbursed to bank account | ✓ |
| `payout.failed` | Payout failed (invalid CLABE, etc.) | ✓ |
| `transfer.succeeded` | Customer-to-customer transfer complete | ✓ [v2] |
| `fee.succeeded` | Platform fee collected | Internal only |
| `spei.received` | SPEI bank transfer received | ✓ |
| `chargeback.created` | Chargeback dispute opened | ✓ |
| `chargeback.accepted` | Chargeback resolved in customer's favour | ✓ |
| `chargeback.rejected` | Chargeback dispute rejected | ✓ |

### Delivery guarantee

```
OpenPay → POST /internal/openpay/events
              │
              ▼
    Atomic write: payment state + webhook_delivery rows (outbox)
              │
              ▼
    Kafka topic: webhook.outbound
              │
              ▼
    Dispatcher worker → POST tenant endpoint  (5 s timeout)
    Retry schedule: 5 s → 30 s → 2 min → 10 min → 1 h → 6 h → 24 h
              │ (all 7 attempts exhausted)
              ▼
    kafka topic: webhook.dlq  →  alert + manual RetryWebhookDelivery RPC
```

Each outbound request is signed with **HMAC-SHA256** (`X-OpenPay-Smart-Signature` header):

```go
payload := fmt.Sprintf("%d.%s", time.Now().Unix(), body)
mac := hmac.New(sha256.New, []byte(subscriptionSecret))
mac.Write([]byte(payload))
header := fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
```

---

## 7. Data Storage Design

### PostgreSQL (primary store)

```sql
-- Core tables (abbreviated)
tenants            (id, merchant_id, openpay_key_enc, webhook_ingress_secret, ...)
members            (id, tenant_id, openpay_customer_id, external_id, email, kyc_status, ...)
payment_links      (id, tenant_id, member_id, amount, currency, token, status, expires_at, ...)
payments           (id, tenant_id, member_id, openpay_transaction_id, order_id,
                    amount, currency, method, status, idempotency_key, metadata JSONB, ...)
payouts            (id, tenant_id, openpay_transaction_id, amount, currency, status, scheduled_for, ...)
disbursement_schedules (tenant_id, frequency, cron_expr, next_run_at, last_run_at, ...)
tenant_card_configs    (id, tenant_id, openpay_card_id, last_four, brand, holder_name, ...)
webhook_subscriptions  (id, tenant_id, url, secret_enc, events TEXT[], retry_policy JSONB, ...)
webhook_deliveries     (id, subscription_id, event_type, payload JSONB,
                        status, attempts, last_attempted_at, response_code, latency_ms, ...)
audit_log              (id, tenant_id, actor, operation, resource_type, resource_id,
                        payload JSONB, created_at)  -- append-only
```

Key indexes:

```sql
CREATE UNIQUE INDEX ON payments (tenant_id, idempotency_key);
CREATE INDEX ON payments (tenant_id, status, created_at DESC);
CREATE INDEX ON payments (member_id, created_at DESC);
CREATE INDEX ON payment_links (token);  -- lookup at redemption time
CREATE INDEX ON webhook_deliveries (subscription_id, status);
```

Row-level security is enabled on all tables with a `tenant_id` column.

### Redis

| Key pattern | TTL | Purpose |
|---|---|---|
| `tenant:{id}:config` | 5 min | Cached tenant config / OpenPay keys |
| `idempotency:{tenantId}:{key}` | 24 h | Deduplication for payment creation |
| `payment:{id}:status` | 30 s | Hot-path status cache |
| `balance:{tenantId}:{memberId}` | 30 s | Balance cache; invalidated on write |
| `ratelimit:{tenantId}:{method}` | 1 min | Sliding window counter |
| `wh:dedup:{eventId}` | 72 h | Webhook delivery deduplication |

### Kafka topics

| Topic | Partitions | Retention | Purpose |
|---|---|---|---|
| `payment.events` | 12 | 7 days | All payment/payout state transitions |
| `disbursement.commands` | 4 | 3 days | Scheduler-issued payout triggers |
| `webhook.outbound` | 12 | 72 h | Pending webhook deliveries |
| `webhook.dlq` | 4 | 14 days | Failed deliveries after all retries |
| `audit.events` | 8 | 90 days | Immutable mutation audit trail |

---

## 8. Cross-Cutting Concerns

### Pagination

All `List*` RPCs use **cursor-based pagination** to avoid offset instability on large tables. The cursor is a base64-encoded `(created_at, id)` pair.

```protobuf
message ListPaymentsRequest {
  string tenant_id  = 1;
  int32  page_size  = 2;  // default 20, max 100
  string page_token = 3;  // opaque cursor from previous response
  PaymentFilter filter = 4;
}
message ListPaymentsResponse {
  repeated Payment payments       = 1;
  string           next_page_token = 2;  // empty = last page
  int32            total_count     = 3;  // approximate
}
```

### Observability

| Pillar | Tool | Key signals |
|---|---|---|
| Structured logs | `zerolog` → stdout (JSON) | `tenant_id`, `rpc_method`, `duration_ms`, `openpay_request_id`, `error_code` |
| Distributed traces | OpenTelemetry + Jaeger | Spans for gRPC handler, OpenPay HTTP call, DB query, Kafka produce |
| Metrics | Prometheus + Grafana | `grpc_duration_seconds`, `openpay_http_duration_seconds`, `webhook_delivery_latency_seconds`, `payment_status_total`, `kafka_consumer_lag` |
| Health checks | gRPC Health protocol | `/healthz` (liveness), `/readyz` (DB + Redis + Kafka probe) |
| Alerting | Alertmanager | p99 > 500 ms, error rate > 1 %, webhook DLQ depth > 10 |

### Rate limiting

Per-tenant sliding window in Redis. Limits returned in gRPC trailers and HTTP headers (`X-RateLimit-*`).

| Tier | Req / min | Burst |
|---|---|---|
| Free | 60 | 20 |
| Standard | 600 | 100 |
| Enterprise | 6 000 | 500 |

### Error codes

| Scenario | gRPC code | HTTP |
|---|---|---|
| Invalid field / missing param | `INVALID_ARGUMENT` | 400 |
| Bad API key | `UNAUTHENTICATED` | 401 |
| Cross-tenant access attempt | `PERMISSION_DENIED` | 403 |
| Resource not found | `NOT_FOUND` | 404 |
| Idempotency key collision | `ALREADY_EXISTS` | 409 |
| OpenPay rate limit hit | `RESOURCE_EXHAUSTED` | 429 |
| OpenPay / DB unavailable | `UNAVAILABLE` | 503 |
| Unhandled internal error | `INTERNAL` | 500 |

OpenPay error codes (e.g. `3001` card declined, `3004` stolen card) are mapped to gRPC error detail extensions (`google.rpc.ErrorInfo`) so callers get actionable error metadata.

---

## 9. Security

### Authentication

- Every inbound gRPC call requires `Authorization: Bearer <tenant_api_key>` metadata
- Keys are stored as SHA-256 hashes; prefixed with `opk_live_` or `opk_test_` for environment clarity
- Outbound calls to OpenPay use the tenant's `openpay_private_key`, stored AES-256-GCM encrypted in the DB, decrypted at runtime via a KMS-derived key

### PCI-DSS scope reduction

The service never transmits or stores raw PANs or CVVs. The card tokenisation flow is:

```
Browser / Mobile → OpenPay JS/Android/iOS SDK → OpenPay Servers
                                                      │
                                              card token (one-time use)
                                                      │
                        SetupTenantCardInfo RPC ◄──────┘
                        stores openpay_card_id only
```

This keeps the service in **SAQ-A / SAQ-A-EP** scope.

### Webhook ingress validation

The internal OpenPay ingress endpoint verifies that requests originate from OpenPay by checking the shared `openpay_ingress_secret` stored per tenant against a header token.

### Data isolation

- All SQL queries include a `tenant_id` predicate at the repository layer
- PostgreSQL RLS policies provide a second enforcement layer
- Kafka topic ACLs restrict each consumer group to its designated topics
- TLS 1.3 mandatory on all network paths; mTLS for inter-service calls

---

## 10. Project Layout

```
openpay-smart-service/
├── cmd/
│   ├── server/           # gRPC server + grpc-gateway entrypoint
│   └── worker/           # Kafka consumers: webhook dispatcher, disbursement scheduler
│
├── internal/
│   ├── api/              # gRPC handler implementations (per service)
│   │   ├── tenant/
│   │   ├── member/
│   │   ├── payment/
│   │   ├── balance/
│   │   └── webhook/
│   ├── domain/           # Core entities, interfaces, business logic
│   ├── repository/       # PostgreSQL implementations (sqlc-generated)
│   ├── openpay/          # OpenPay REST client (charges, customers, payouts, cards…)
│   ├── cache/            # Redis wrappers (idempotency, balance cache, rate limiter)
│   ├── events/           # Kafka producer/consumer wrappers
│   ├── webhook/          # Delivery engine, HMAC signer, retry logic, DLQ handler
│   ├── scheduler/        # Disbursement cron engine
│   ├── middleware/        # Auth interceptor, rate limiter, request logger, tracing
│   └── telemetry/        # OpenTelemetry bootstrap
│
├── proto/
│   └── openpay/v1/
│       ├── tenant.proto
│       ├── member.proto
│       ├── payment.proto
│       ├── balance.proto
│       ├── webhook.proto
│       └── common.proto   # shared enums: PaymentStatus, Currency, PaymentMethod…
│
├── migrations/            # SQL migrations (goose)
├── config/                # YAML / env config schema
├── deploy/
│   ├── k8s/               # Kubernetes manifests
│   └── helm/              # Helm chart
├── docs/
│   └── adr/               # Architecture Decision Records
└── Makefile
```

---

## 11. Configuration

```yaml
# config/config.yaml
server:
  grpc_port: 50051
  http_port: 8080
  tls_cert_file: /certs/server.crt
  tls_key_file:  /certs/server.key

openpay:
  environment: sandbox        # sandbox | production
  sandbox_base_url: https://sandbox-api.openpay.mx/v1
  prod_base_url:    https://api.openpay.mx/v1
  http_timeout_ms:  10000
  max_retries:      3

database:
  dsn: postgres://user:pass@localhost:5432/openpay_smart?sslmode=require
  max_open_conns: 25
  max_idle_conns: 5

redis:
  addr: localhost:6379
  db:   0

kafka:
  brokers: [localhost:9092]
  consumer_group: openpay-smart-workers

disbursement:
  default_frequency: daily
  default_cron: "0 18 * * *"   # 18:00 UTC
  min_interval_hours: 1

webhook:
  dispatch_timeout_ms: 5000
  max_attempts:        7
  retry_intervals_sec: [5, 30, 120, 600, 3600, 21600, 86400]

telemetry:
  jaeger_endpoint: http://jaeger:14268/api/traces
  prometheus_port: 9090
  log_level: info
```

Environment variables override any YAML key using the `OPENPAY_` prefix (e.g. `OPENPAY_DATABASE_DSN`).

---

## 12. Getting Started

### Prerequisites

- Go 1.22+
- Docker + Docker Compose
- `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc`
- `grpc-gateway` (`protoc-gen-grpc-gateway`)
- `goose` for DB migrations
- `sqlc` for query codegen (optional but recommended)

### Run locally

```bash
# 1. Clone & bootstrap
git clone https://github.com/your-org/openpay-smart-service
cd openpay-smart-service

# 2. Start dependencies (Postgres, Redis, Kafka, Jaeger)
docker compose up -d

# 3. Run migrations
goose -dir migrations postgres "$DATABASE_DSN" up

# 4. Generate proto stubs
make proto

# 5. Start the gRPC server
go run ./cmd/server

# 6. Start the Kafka worker (separate terminal)
go run ./cmd/worker
```

### Run tests

```bash
make test          # unit tests
make test-int      # integration tests (requires Docker Compose up)
```

### Generate OpenPay sandbox credentials

1. Create a free account at [dashboard.openpay.mx](https://dashboard.openpay.mx)
2. Copy the **Merchant ID** and **Private API Key** from the sandbox dashboard
3. Set them in your config or environment:
   ```bash
   export OPENPAY_OPENPAY_SANDBOX_MERCHANT_ID=mzdtln0bmtms6o3kck8f
   export OPENPAY_OPENPAY_SANDBOX_PRIVATE_KEY=sk_xxxxxxxxxxxxxxxx
   ```

---

## 13. Implementation Phases

| Phase | Milestone | Scope | Target |
|---|---|---|---|
| **0** | Foundation | Repo scaffold, proto definitions, DB migrations, CI pipeline, auth interceptor, OpenPay HTTP client | Week 1–2 |
| **1** | Core Payments | `TenantService` (card setup, frequency), `MemberService` (create/link), `PaymentService` (status + list), OpenPay charge flow | Week 3–5 |
| **2** | Balances & Events | `BalanceService`, Kafka `payment.events` producer, streaming RPC, audit log | Week 6–7 |
| **3** | Webhooks | OpenPay ingress endpoint, `WebhookService` CRUD, delivery engine, retry worker, DLQ, HMAC signing | Week 8–9 |
| **4** | Hardening | Rate limiting, idempotency, cursor pagination, error detail mapping, Grafana dashboards, load testing | Week 10–11 |
| **5** | v2 Features | Refunds, balance history, manual disbursement, recurring subscriptions (plans), payment receipts, fee reporting | Week 12+ |

---

## 14. Non-Functional Requirements

| NFR | Target | How measured |
|---|---|---|
| Availability | 99.9 % monthly | Synthetic canary + Prometheus `up` probe |
| Read p99 latency | < 50 ms (`GetPaymentStatus`) | gRPC histogram |
| Write p99 latency | < 200 ms (payment creation incl. OpenPay call) | gRPC histogram |
| List p99 latency | < 300 ms (`ListPayments`, 1 k rows) | gRPC histogram |
| Throughput | 10 000 RPC / s peak | k6 gRPC load test |
| Webhook delivery | 95 % within 60 s of OpenPay event | `webhook_delivery_latency_seconds` |
| Idempotency window | 24 h | Redis TTL |
| RPO | < 1 min | PostgreSQL streaming replication + WAL archiving |
| RTO | < 5 min | Patroni automated failover |

---

## References

- [OpenPay API Reference (EN)](https://documents.openpay.mx/en/api)
- [OpenPay Webhooks Guide](https://documents.openpay.mx/en/docs/webhooks.html)
- [OpenPay BBVA eCommerce API](https://docs.ecommercebbva.com/)
- [OpenPay cURL Reference](https://documents.openpay.mx/en/docs/curl.html)
- [google.golang.org/grpc](https://pkg.go.dev/google.golang.org/grpc)
- [grpc-gateway](https://grpc-ecosystem.github.io/grpc-gateway/)
- [pressly/goose](https://github.com/pressly/goose)
