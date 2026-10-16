# Audit consumer on Kubernetes

The worker needs a Deployment, not a Service or Ingress. Kubelet probes the Pod
directly on port 8084. `/readyz` returns 200 after the consumer joins its
Kafka group and PostgreSQL responds, even if the worker owns no partitions.
If it owns partitions, readiness also requires a successful poll or commit within
`HEALTH_READY_MAX_AGE` (90 seconds by default). It returns 503 before joining,
when PostgreSQL is unavailable, when an assigned worker stops making progress,
or during shutdown. It gates rollout after 30 stable seconds; it does not stop
Kafka consumption. `/healthz` reports that the HTTP listener is alive, but is
not a liveness probe: fatal consumer errors exit the process and the Deployment
restarts it.

`HEALTH_HTTP_ADDR` defaults to `:8084`. If you override its port, change the
Deployment container port and Kuma monitor URL to match.

Before applying `deployment.yaml`:

1. Build `audit/Dockerfile`, publish the image, and replace its image reference
   with the published immutable digest.
2. Create `audit-consumer-config` with `SERVICE_ENV=production`,
   `POSTGRES_MIGRATE=false`, `KAFKA_BROKERS`, `KAFKA_CONSUMER_GROUP`,
   `KAFKA_CONSUMER_TOPICS`, `KAFKA_CONSUMER_DLQ_TOPIC`, and
   `OTEL_EXPORTER_OTLP_ENDPOINT` for this environment. See `.env.example` for
   the other optional settings. Apply database migrations before the rollout.
3. Create `audit-consumer-secrets` with `POSTGRES_DSN` (and any other secret
   environment variables). Restrict access to that Secret.
4. Set the Kafka brokers, PostgreSQL, and OTLP collector to addresses reachable
   from the Pod. Allow kubelet to reach Pod port 8084; do not publish it via a
   Service or Ingress.
5. Measure memory and processing time under real load and tune resource values
   and the 120-second termination grace period. A forced termination may replay
   an uncommitted record; the audit write is idempotent by event ID.

During rollout, watch Deployment progress, Pod restarts, consumer lag, commit
failures, and DLQ records. A successful idle poll proves the client can poll;
it does not prove that the group is keeping up with incoming records. Alert on
lag and DLQ separately. Avoid a liveness probe that queries Kafka or Postgres:
an external outage must not cause every worker to restart.
