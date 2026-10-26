# paygate

Personal QRIS payment gateway. Turns a static Shopee merchant QRIS into a
per-order dynamic QRIS, tracks whether each order was paid, and reconciles the
result against the provider's transaction feed.

## How the money stays exact

A unique-amount offset alone would make every price drift by Rp 1–999. Instead
the order id travels inside the QRIS payload itself:

- tag `62/05` (Reference Label) carries the order's public id, e.g.
  `ORD-20261026-40DA56C5`
- tag `54` carries the exact amount

The offset is still allocated and added to the total, but it exists only as a
fallback for providers that drop the reference. Because the reference is the
primary key of the match, a payment can be attributed without guessing, and an
ambiguous or unknown transaction is never auto-settled.

## Layout

```
backend/    Go gateway: HTTP API, QRIS codec, reconciliation, Postgres store
web/        Next.js dashboard (shadcn, Tailwind 4)
ops/        Windows run script
docker-compose.yml   Postgres 5433, adminer 8081
```

## Requirements

- Go 1.24+
- Node 22, pnpm 10
- Docker

## Run

```powershell
docker compose up -d
Copy-Item .env.example .env      # then fill in the secrets
.\ops\run.ps1                    # gateway on 127.0.0.1:8787
```

```powershell
cd web
pnpm install
pnpm dev                         # dashboard on :3000
```

The dashboard proxies `/api/*` to the gateway so the session cookie stays
first-party. `PAYGATE_BACKEND` overrides the target.

## Trust model

Three rules are enforced in code, not left to the provider SDK:

1. **Fail closed.** A transaction whose status cannot be classified is
   `pending`, never `settled`. Upstream treats an empty status as success.
2. **Capture is not settlement.** Authorised funds route to `review`, not
   `paid`.
3. **Amount is not proof.** A match also needs a time window. Two candidates
   sharing an amount inside that window go to `review` for a human.

Unmatched transactions are never dropped: they persist with `order_id IS NULL`
and surface on the Payments page for manual attribution.

## Verification

```powershell
cd backend
go build ./... && go vet ./... && go test ./...

cd ../web
pnpm lint && pnpm exec tsc --noEmit && pnpm build
```

## Status

Verified end to end against a local stack: order creation, QR PNG/SVG
rendering, SSE status stream, cancel, manual match, offset exhaustion, and
order expiry. The Shopee OTP login and the live transaction feed are **not**
verified — they require a real account and a captured device-risk blob, so the
reconciliation worker has never run against live data.
