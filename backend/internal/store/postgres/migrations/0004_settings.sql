-- 0004_settings.sql — key/value settings that must survive a restart.
--
-- Static QRIS is the important one: it is the merchant identity every dynamic
-- payload is derived from. Losing it on restart would silently stop the gateway
-- from issuing QRs, so it lives in the database, not in memory.

BEGIN;

CREATE TABLE settings (
    key        TEXT        PRIMARY KEY,
    value      TEXT        NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
