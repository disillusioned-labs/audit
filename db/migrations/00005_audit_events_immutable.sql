-- +goose Up
-- audit_events is append-only, enforced by the database rather than by the
-- absence of a query: every UPDATE and DELETE raises, regardless of which
-- role executes it. Retention is a purge job decision, not an ad-hoc
-- mutation - if retention ever arrives it must run as superuser explicitly
-- dropping partitions/rows with this trigger disabled by a reviewed change.
CREATE OR REPLACE FUNCTION prevent_audit_event_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only: % blocked', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_events_immutable
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION prevent_audit_event_mutation();

-- +goose Down
DROP TRIGGER IF EXISTS audit_events_immutable ON audit_events;
DROP FUNCTION IF EXISTS prevent_audit_event_mutation();
