-- +goose Up
CREATE TABLE audit_events
(
    id             UUID PRIMARY KEY     DEFAULT uuidv7(),
    event_id       UUID        NOT NULL,
    event_type     TEXT        NOT NULL,
    event_version  INTEGER     NOT NULL,
    source_service TEXT        NOT NULL,
    actor_type     TEXT,
    actor_id       UUID,
    aggregate_type TEXT        NOT NULL,
    aggregate_id   UUID        NOT NULL,
    tenant_id      UUID,
    status         TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address     INET,
    user_agent     TEXT,
    trace_id       TEXT,
    details        JSONB,

    CONSTRAINT uq_audit_events_event_id UNIQUE (event_id)
);

-- +goose Down
DROP TABLE IF EXISTS audit_events;

-- id              → ID internal audit record (UUIDv7)
-- event_id        → ID event dari producer/outbox
-- event_type      → auth.user_logged_in, dll
-- event_version   → version event contract
-- source_service  → identity, billing, payment, dll
--
-- actor_type      → user / service / system
-- actor_id        → siapa yang melakukan action
--
-- aggregate_type  → user / organization / invoice / dll
-- aggregate_id    → entity yang terkait dengan event
--
-- tenant_id       → tenant/organization context
--
-- status          → success / failure / denied / dll
--
-- created_at      → kapan Audit Service menyimpan record
--
-- ip_address      → IP client jika tersedia
-- user_agent      → user agent jika tersedia
-- trace_id        → OpenTelemetry distributed trace ID
--
-- details         → event-specific structured data