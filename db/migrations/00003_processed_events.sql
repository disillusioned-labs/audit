-- +goose Up
CREATE TABLE processed_events
(
    event_id       UUID PRIMARY KEY,
    event_type     TEXT        NOT NULL,
    event_version  INTEGER     NOT NULL,
    processed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS processed_events;