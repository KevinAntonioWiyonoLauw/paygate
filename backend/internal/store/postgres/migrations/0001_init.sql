-- 0001_init.sql — core schema for the paygate QRIS gateway.
--
-- Design notes that matter for money safety:
--   * `payments.provider_tx_id` is UNIQUE per provider. That is the idempotency
--     guard: a feed re-delivery can never settle an order twice.
--   * `payments.order_id` is nullable on purpose. A transaction we cannot
--     attribute goes to suspense (order_id IS NULL) instead of being dropped.
--   * `orders.status` separates `paid` from `review`, so a `capture` (funds held,
--     not settled) never triggers fulfilment.
--   * `amount_reservations` lives in the DB, not in process memory, so a restart
--     cannot recycle an offset that a late payment may still hit.

BEGIN;

CREATE TYPE order_status AS ENUM (
    'pending',    -- QR issued, waiting for money
    'paid',       -- settled, safe to fulfil
    'review',     -- money seen but ambiguous (capture, duplicate candidate, out of window)
    'expired',    -- window closed without payment
    'cancelled'   -- cancelled by operator
);

CREATE TYPE payment_status AS ENUM (
    'pending',    -- seen in feed, not final
    'captured',   -- authorised / held, not settled
    'settled',    -- money actually received
    'failed',
    'refunded'
);

CREATE TYPE order_source AS ENUM ('manual', 'api', 'seed');

-- ---------------------------------------------------------------------------
-- orders
-- ---------------------------------------------------------------------------
CREATE TABLE orders (
    id            BIGSERIAL      PRIMARY KEY,
    public_id     TEXT           NOT NULL UNIQUE,
    base_amount   BIGINT         NOT NULL CHECK (base_amount > 0),
    unique_offset INT            NOT NULL DEFAULT 0 CHECK (unique_offset >= 0),
    total_amount  BIGINT         NOT NULL CHECK (total_amount > 0),
    status        order_status   NOT NULL DEFAULT 'pending',
    source        order_source   NOT NULL DEFAULT 'manual',
    provider      TEXT           NOT NULL DEFAULT 'shopee',
    qr_payload    TEXT           NOT NULL DEFAULT '',
    reference     TEXT,
    description   TEXT,
    metadata      JSONB          NOT NULL DEFAULT '{}'::jsonb,
    expires_at    TIMESTAMPTZ    NOT NULL,
    paid_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ    NOT NULL DEFAULT now(),

    CONSTRAINT orders_total_matches CHECK (total_amount = base_amount + unique_offset)
);

-- The hot query is "which pending orders could this transaction settle".
CREATE INDEX orders_pending_idx ON orders (status, expires_at) WHERE status = 'pending';
-- Amount lookup during matching.
CREATE INDEX orders_amount_idx ON orders (total_amount, created_at DESC);
-- Newest-first listing in the UI.
CREATE INDEX orders_created_idx ON orders (created_at DESC);
CREATE INDEX orders_status_created_idx ON orders (status, created_at DESC);

-- ---------------------------------------------------------------------------
-- payments — one row per provider transaction observed in the feed
-- ---------------------------------------------------------------------------
CREATE TABLE payments (
    id             BIGSERIAL      PRIMARY KEY,
    provider       TEXT           NOT NULL,
    provider_tx_id TEXT           NOT NULL,
    gross_amount   BIGINT         NOT NULL,
    real_amount    BIGINT,
    status         payment_status NOT NULL DEFAULT 'pending',
    tx_time        TIMESTAMPTZ,
    order_id       BIGINT         REFERENCES orders(id) ON DELETE SET NULL,
    match_rule     SMALLINT,
    raw            JSONB          NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),

    -- Idempotency: the same provider transaction can never be inserted twice.
    CONSTRAINT payments_provider_tx_unique UNIQUE (provider, provider_tx_id)
);

CREATE INDEX payments_order_idx ON payments (order_id);
-- Suspense list: unmatched rows.
CREATE INDEX payments_suspense_idx ON payments (created_at DESC) WHERE order_id IS NULL;
CREATE INDEX payments_tx_time_idx ON payments (tx_time DESC);

-- ---------------------------------------------------------------------------
-- amount_reservations — the unique-amount allocator, durable
-- ---------------------------------------------------------------------------
CREATE TABLE amount_reservations (
    base_amount    BIGINT      NOT NULL CHECK (base_amount > 0),
    -- "offset" is a reserved word in Postgres, so the column is named slot.
    slot           INT         NOT NULL CHECK (slot > 0),
    order_id       BIGINT      REFERENCES orders(id) ON DELETE CASCADE,
    reserved_until TIMESTAMPTZ NOT NULL,
    -- Set when the slot actually settled. Drives the cooldown window.
    last_used_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (base_amount, slot)
);

CREATE INDEX amount_reservations_order_idx ON amount_reservations (order_id);

-- ---------------------------------------------------------------------------
-- feed_cursors — survive restarts without rescanning or skipping
-- ---------------------------------------------------------------------------
CREATE TABLE feed_cursors (
    provider       TEXT        PRIMARY KEY,
    cursor         TEXT,
    last_tx_time   TIMESTAMPTZ,
    last_polled_at TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- provider_sessions — encrypted Shopee session blobs
-- ---------------------------------------------------------------------------
CREATE TABLE provider_sessions (
    provider      TEXT        PRIMARY KEY,
    -- AES-256-GCM ciphertext, base64. Never store the plaintext session.
    payload_enc   TEXT        NOT NULL,
    account_id    TEXT,
    merchant_id   TEXT,
    merchant_name TEXT,
    store_id      TEXT,
    store_name    TEXT,
    expires_at    TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- admin_users — single-user auth for the dashboard
-- ---------------------------------------------------------------------------
CREATE TABLE admin_users (
    id            BIGSERIAL   PRIMARY KEY,
    username      TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

-- ---------------------------------------------------------------------------
-- outbox — downstream webhook delivery, retried until delivered
-- ---------------------------------------------------------------------------
CREATE TABLE outbox (
    id           BIGSERIAL   PRIMARY KEY,
    event        TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    attempts     INT         NOT NULL DEFAULT 0,
    last_error   TEXT,
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX outbox_pending_idx ON outbox (next_retry_at) WHERE delivered_at IS NULL;

-- ---------------------------------------------------------------------------
-- audit_log — every manual action on money
-- ---------------------------------------------------------------------------
CREATE TABLE audit_log (
    id         BIGSERIAL   PRIMARY KEY,
    actor      TEXT        NOT NULL,
    action     TEXT        NOT NULL,
    entity     TEXT        NOT NULL,
    entity_id  TEXT,
    detail     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);

COMMIT;
