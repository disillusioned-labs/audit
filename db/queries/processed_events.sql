-- name: IsEventProcessed :one
SELECT EXISTS (
    SELECT 1
    FROM processed_events
    WHERE event_id = $1
) AS processed;


-- name: CreateProcessedEvent :one
INSERT INTO processed_events (
    event_id,
    event_type,
    event_version
)
VALUES (
           $1,
           $2,
           $3
       )
    ON CONFLICT (event_id) DO NOTHING
RETURNING *;