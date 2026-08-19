# AGENTS.md

Guidance for AI coding agents (Claude Code, Cursor, Copilot, Codex, ...)
working in this repository.

## Commands

```bash
cp .env.example .env        # required first: defaults are production-safe, so no .env means no boot-time migrations
psql -U postgres -h localhost -f ../scripts/init-db.sql   # once per machine: creates the identity/expense databases and roles
docker compose up -d        # postgres(5433) redis(6380) jaeger prometheus otel-collector
go run ./cmd/api            # run the API (applies goose migrations on boot when .env sets POSTGRES_MIGRATE=true)
make test                   # unit tests with -race
make test-integration       # integration tests (needs Docker; -tags integration)
make lint                   # golangci-lint (CI pins v2.12.2 - keep local in sync)
make sqlc                   # regenerate internal/repository after editing db/queries/
make sqlc-diff              # same staleness check CI runs
make migrate-new name=foo   # create a new goose migration in db/migrations/
make vuln                   # govulncheck at the pinned version
```

Run a single test: `go test -run TestName ./internal/service/auth/`.
Integration tests live behind the `integration` build tag so plain `go test ./...` stays Docker-free.

`docker-compose.yml` describes what a server runs, and maps Postgres to host 5433 and Redis to 6380 so a containerised pair never collides with a native one - never change those mappings.

**On this development machine, Postgres and Redis are the native installs on the stock 5432/6379, and the compose pair is left down** to save memory. `.env` is what selects between them, so only `.env` changes - never the compose file. Bringing the containers up as well is harmless; they simply go unused.

One Postgres instance, two logical databases: `identity` and `expense`, each with its own role. That is what makes the service boundary real - cross-database joins are impossible in Postgres, so `expense` cannot reach an `identity` table even by accident. The setup script lives one level up, outside this repository, because it creates both databases and so belongs to neither service. It also revokes `CONNECT` from `PUBLIC` on both, without which either role could still open a session against the other's database (Postgres grants CONNECT to PUBLIC by default).

On this Windows machine `-race` fails (`cc1.exe: 64-bit mode not compiled in` - 32-bit gcc); run tests without `-race` locally and let CI cover the race detector. The same 32-bit gcc breaks `sqlc` unless CGO is off - the Makefile already sets `CGO_ENABLED=0` for it.

Tool versions (goose, sqlc, govulncheck) are pinned as Makefile variables, **not** `go.mod` tool directives: sqlc's dependency tree (wazero, the tidb parser, cel-go) would otherwise be pulled by the Dockerfile's `go mod download` on every image build. Bump them in the Makefile and `.github/workflows/ci.yml` together.

## Architecture

Request flow: `internal/handler/<resource>` → `internal/service/<resource>` → `internal/repository` (sqlc/pgx). The graph is **flat and exactly three layers deep**: a service calls the repository and nothing else. There is no service→service edge anywhere, and adding one is the single change most likely to be rejected in review - see the invariant below for why.

**The unit of a service is a use case, not a table.** A service package is named after the endpoint group it serves (`service/auth` backs `handler/auth`), and it reaches every table that use case touches. `Register` writes `users`, `organizations`, `organization_members` and then updates `users.last_active_organization_id`, all inside one `ExecTx` - so `authService` issues all four queries itself, straight against `repository.Querier`. It does not ask a `userService` to create the user for it, because that is exactly the edge the invariant forbids.

The price is duplication, and it is worth naming out loud: the day a standalone user endpoint arrives, `CreateUser` will be called from `service/user` as well as `service/auth`, and the same row will be mapped to a service type in two places. **That is bought deliberately, not overlooked.** What it buys is a dependency graph with no internal edges to reason about and a transaction boundary you can see in one file - the alternative is a mesh where "who owns this transaction" is answered by reading four packages, and where a shared helper quietly grows a second caller with different transactional needs. When you find yourself copying a mapping for the second time, copy it. Extract only on the third, and extract it as a pure function over a sqlc row - never as a service one service calls.

**Adding a resource:**
1. `make migrate-new` → write SQL in `db/migrations/` (goose format, up+down in one file)
2. Add queries in `db/queries/` → `make sqlc` (sqlc reads the migrations dir as schema)
3. Add `internal/service/<usecase>/` (`model.go` + `service.go`) and `internal/handler/<usecase>/`, using `auth` as the shape reference - including the names: `<Resource>Service` / `New<Resource>Service` / `<resource>Service` (see Conventions). Query the tables the use case needs directly; do not reach for another service.
4. Wire it in `internal/app/di.go` (add construction) and `internal/server/server.go` (add a `Deps` field + `r.Mount`). `app.go`, `cmd/` and `internal/platform/` never change.

Adding a resource must not require editing `internal/handler/respond.go` or `internal/service/errors.go`. If it does, the error model has regressed - see below.

**Non-negotiable invariants:**
- **A service never calls another service.** `internal/service/<x>` may import `internal/repository` and `internal/platform/...`, never `internal/service/<y>`. The dependency graph stays flat - handler → service → repository - so the only question a reader ever has to answer about a call is "which tables does this touch", not "which service touches which service". A use case that spans several tables issues all of those queries itself.
- **Services take `querier repository.Querier` as a method parameter; they do not hold a repository field.** Every method that could ever participate in a caller's transaction takes the querier it should run on. Only the service that *owns* a transaction keeps `repo repository.Store` as a field, and it uses it for one thing: calling `ExecTx` and handing the resulting `Querier` down. Because `Store` embeds `Querier`, an unenlisted call is just `s.repo` passed as the parameter - there is no second code path and no "transactional variant" of any method.

  This is not theoretical tidiness; it is a bill this repo already paid. `jwtService` originally stored a `repo Store` field and ran its own queries against it. That was fine until refresh-token rotation arrived, where revoking the old token and inserting the new one must be atomic: the stored field bound every query to the pool, so the operation could not join the caller's transaction, and fixing it meant changing the interface, the constructor and every call site at once. A method parameter would have cost nothing up front and made rotation a non-event. Take the parameter from the first method, before you know which caller will need a transaction.
- Every HTTP response goes through `internal/handler/respond.go` (`OK`/`OKList`/`WriteError`/`WriteServiceError`). Never write to the ResponseWriter directly; never use `http.Error`. Envelope shape: `{"data": ...}` / `{"error": {"code", "message", "fields"?}}` with machine-readable codes.
- `WriteJSON` marshals into a buffer *before* writing any header. Never encode straight to the ResponseWriter: that commits a 200 first, so a mid-stream marshal failure would ship a truncated body under a success status.
- **Domain errors are self-describing.** `service.Error` carries `Code`, `Status` and `Message`; `WriteServiceError` reads those via `errors.As` and therefore contains *no* per-resource cases. New resource-specific errors are declared with `service.NewError(...)` in that resource's own package - never by adding a case to the shared handler switch. Services wrap with `%w` so the mapping survives.
- Context errors are checked **before** the domain error in `WriteServiceError`: an expired request is a 504 and a cancelled one writes nothing, because a timeout is a transport outcome and not a server fault. `chimw.Timeout` only cancels the context - this mapping is what actually produces the 504.
- Request decoding goes through `handler.DecodeValid[T]` - it enforces the 1 MiB body cap, unknown-field rejection, and `validate` struct tags.
- List endpoints read pagination through `handler.DecodePage`, which **rejects** a malformed or out-of-range `limit`/`offset` with 422 instead of silently clamping. Silent clamping hands the client a page it never asked for and contradicts `DecodeValid`'s strictness on the body path.
- Writes that report rows-affected map 0 rows to `service.ErrNotFound` (see `SetLastActiveOrganization :execrows` and `RevokeRefreshToken :execrows`) rather than answering a silent 204.
- Config: every knob is an environment variable + a default **and validation** in `internal/config`, documented in `.env.example`. Never read env vars directly outside that package. `validate()` accumulates every problem with `errors.Join` so one boot surfaces the whole list. Two CI tests keep `.env.example` honest: it must produce a valid config, and every key in it must be one the app actually reads (a typo there is invisible at runtime - the setting is simply ignored forever).
- `config.Load()` is called in `cmd/api/main.go`, not in `app.Run` - `Run(cfg *config.Config)` takes a ready config so it stays callable from a test or a second binary with a config built in code, no file and no environment.
- Anything holding a secret implements `slog.LogValuer` (`PostgresConfig`, `RedisConfig`). `log.Info("...", "postgres", cfg.Postgres)` must never be able to print a password.
- **`internal/platform/` holds generic infrastructure adapters** (`postgres`, `redis`, `cache`, `telemetry`). They take plain values plus functional options and know nothing about this application, so tests and tools can construct them without a `config.Config`. **This is enforced, not documented**: a `depguard` rule fails the build if anything under `internal/platform/` imports `github.com/disillusioned-labs/audit/internal`. That rule is the whole justification for the directory - without it, `platform/` becomes the dumping ground every such directory turns into. Don't add something there that needs app knowledge; resolve the decision in `app.go` and pass it down as data.
- **`internal/platform/jwt` is a component, not a service, and that is why it is not a hole in the no-service-calls-a-service rule.** It is pure cryptography: sign an RS256 JWT, parse an RSA private key from PEM, generate a refresh token and hash it. It knows nothing about users, organizations or this application, and - like everything under `internal/platform/` - imports nothing from `internal/`, which the existing depguard rule already enforces. The signing key is **loaded from the database by the service** and handed to `platform/jwt` as an `*rsa.PrivateKey`; the package never sees a `Querier` and never touches the repository. Read the dependency direction as the test: a service calling `platform/jwt` is a service calling a library, the same as calling `bcrypt`. If something you are about to put there needs to read a row, it is not a component - it belongs in the service that owns the use case.
- Only the composition layer imports `internal/config`: `cmd/api` (loads it), `app/` (unpacks it) and `server/` (app-specific by nature). A second `depguard` rule denies `internal/config` to `handler/`, `service/` and `repository/`. Policy decisions (is tracing on, is Redis required) are resolved in `app.go` and passed down as data, not re-read from config downstream.
- Cache is nilable by design: `cache.Cache` interface, nil means run uncached (redis.mode=disabled/optional). Keep nil checks when touching services; never pass a typed-nil `*cache.Cache` (see the `setupRedis` comment in app.go).
- Transactions: any use case that writes more than one row goes through `repository.Store.ExecTx`, and read-modify-write takes `FOR UPDATE` inside it. The `ExecTx` call sits in the service that owns the use case (`authService.Register`, `authService.Refresh`) - never in a helper a second service also calls, because a nested or reused transaction owner is how two callers end up with different atomicity guarantees from the same code.
- `internal/repository` is sqlc-generated except `store.go` - never hand-edit generated files; CI's `sqlc diff` job fails on drift.

**Deliberate decisions - do not "fix" these:**
- No RealIP middleware: forwarded headers are spoofable; rate limiting keys off the TCP peer (`RemoteAddr`). Deployments behind a trusted proxy add their own middleware.
- The rate limiter counts **in-process**: N replicas allow N×`ratelimit.requests`. That is documented, not overlooked - swap in `httprate-redis` before treating it as a hard global cap.
- No swagger, mock generators, CORS - out of scope by decision. (Auth is no longer on that list: it is this service's reason to exist.)
- The app is intentionally NOT in docker-compose (infra only); it runs natively for fast iteration. `Dockerfile` is the production image.
- goose over golang-migrate; migrations are embedded (`db/migrations/migrations.go`) and auto-applied at boot when `POSTGRES_MIGRATE=true` - which `.env.example` sets for dev only. The default is off, so production migrates from CI unless explicitly told otherwise.
- Offset pagination is the default because it is what a fresh project needs. It degrades past roughly 10k rows - switch that resource to keyset pagination then; `handler.Page.Meta()` is the one place the response contract is built.
- Build provenance lives in `internal/app/version.go` (unexported vars + `buildInfo()`), not its own package: `app` is the only consumer and the vars encode no decision. `debug.ReadBuildInfo` is not the source because `.dockerignore` excludes `.git`, so a container build has no VCS data. Values arrive via `-ldflags -X github.com/disillusioned-labs/audit/internal/app.version=…` - the Makefile and Dockerfile must agree on that path.
- `logger` and `observability` are one package (`platform/telemetry`) because they are one seam, not two: the logger's handler reads the active span to stamp `trace_id`/`span_id`, so log correlation only works when both are configured consistently. The colliding `Option` types are disambiguated as `LogOption` (for `NewLogger`) and `Option` (for `Setup`); call sites stay `telemetry.Format(...)` / `telemetry.WithTracing(...)` / `telemetry.WithMetrics(...)`.

**There is no `/metrics` endpoint, and adding one back is a regression.** Metrics are pushed over OTLP to a collector, which re-exposes them for Prometheus. A scrape endpoint would be a second, unauthenticated egress path for the same data, on the public port, revealing route patterns, latency distributions and pool sizes. `TestNoMetricsEndpointOnPublicRouter` asserts a 404.

The cost of push is paid deliberately: **there is no `up`.** Nothing polls the app, so no scrape can fail, and a dead app is indistinguishable from a dead collector or a broken network between them. `deploy/rules.yml` says so out loud - `AppMetricsMissing` fires on `absent(go_goroutine_count)` and `CollectorDown` (the collector *is* scraped) is what tells the two apart. Restarting a wedged process is the liveness probe's job, not an alert's.

**pprof** is the one remaining private listener: hardcoded to `127.0.0.1:PPROF_PORT`, off by default (`PPROF_ENABLED=false`). Disabled means `NewPprofServer` returns nil and `app.go` starts no goroutine and opens no socket - being off is an **attack-surface** decision, not a performance one, since pprof samples nothing until an endpoint is requested. There is deliberately **no bind-host knob** - reach it with `kubectl port-forward`, never by widening the bind. Tests assert it is absent from the public router, that the private listener is loopback, and that disabled returns nil.

**Defaults are the production-safe set, not the convenient set.** A deployment ships no file, so `internal/config`'s defaults are what it actually runs on: `POSTGRES_MIGRATE=false`, `LOG_FORMAT=json`, `LOG_LEVEL=info`, `PPROF_ENABLED=false`. `.env.example` is the dev-only opt-in to the convenient values (debug text logs, migrate-on-boot). When adding a knob: register it in `setDefaults` with the value production wants - registration is mandatory, since Viper's `AutomaticEnv` only resolves keys it already knows - validate it, then add the dev value to `.env.example`.

**Variable names are unprefixed, so check every new key against the shared namespace.** Kubernetes injects `<SERVICE>_PORT` and `<SERVICE>_SERVICE_HOST` for every Service in the namespace (`enableServiceLinks`, on by default), so a key that spells out to `POSTGRES_PORT` or `REDIS_PORT` would be silently overwritten with something like `tcp://10.96.0.1:5432`. Current keys (`POSTGRES_DSN`, `REDIS_ADDR`, ...) avoid that; keep it that way.

**`service.*`** is named defensively and must not be "simplified" back: bare `NAME` and `ENV` are generic enough that unrelated tooling sets them.

**`otel.*` does the opposite on purpose: it adopts the OpenTelemetry SDK's own variable names with the SDK's own meaning** (`OTEL_SDK_DISABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_TRACES_SAMPLER`, ...). An earlier revision used a private `TRACING_*` namespace to avoid squatting; adopting the real names avoids the same failure more directly, because an operator who sets `OTEL_EXPORTER_OTLP_ENDPOINT` from memory now gets what they expect instead of being ignored. Taking the spec's names means owing the spec's semantics - three consequences that are easy to break:
- `OTEL_METRIC_EXPORT_INTERVAL` is an integer of **milliseconds**, not a Go duration like every other interval in `internal/config`. `TestMetricExportIntervalIsMilliseconds` pins it; reading "60000" as a duration would mean 60µs.
- `OTEL_EXPORTER_OTLP_ENDPOINT` **requires a scheme** - it is what selects TLS. A bare `host:4317` is rejected at boot with the fix in the message, because it is what everyone types first.
- `OTEL_TRACES_SAMPLER` accepts only the strategies `telemetry.NewSampler` implements; `jaeger_remote` and `xray` are **rejected, not ignored**. A sampler that silently is not the one you asked for is how a trace bill or an empty incident timeline happens.

`OTEL_SERVICE_NAME` is deliberately **not** read: `SERVICE_NAME` owns the service identity because the logger uses it too, and it is documented in `.env.example` rather than left to surprise someone. Anything unregistered (`OTEL_EXPORTER_OTLP_HEADERS`, `_TIMEOUT`, `_COMPRESSION`) still reaches the exporter from the **real** environment, since the SDK reads those itself - but not from `.env`, which is parsed into Viper and never exported to the process.

**There is one config vocabulary: environment variables.** An earlier revision had a `config.yaml` of dotted keys that production never read, so every knob had two spellings and the env names were documented nowhere. `.env` is loaded by `parseDotEnv` into a map and layered in as Viper *defaults* - never via `os.Setenv`, which would make `Load` non-idempotent and leak between tests. Because `AutomaticEnv` resolves before defaults, a real environment variable still wins for free: **environment > .env > defaults**.

**Shutdown ordering matters** (`app.go`): a signal fires → `stop()` restores default signal handling so a second Ctrl-C can still kill the process → `srv.BeginDrain()` flips `/readyz` to 503 while traffic is still served → wait `server.drain_delay` → `Shutdown`. The drain gap exists because Kubernetes removes endpoints asynchronously; closing the listener the instant SIGTERM lands is the usual source of 502s during a rolling deploy. Liveness keeps returning 200 throughout - a liveness failure tells the orchestrator to *kill* the process mid-drain.

**Probes are excluded from both signals.** `otelhttp.WithFilter(!isProbe)` in `server.New` drops `/healthz` and `/readyz` - it short-circuits before any instrumentation, so metrics go too, which is wanted: constant always-fast probe traffic drags the p99 down and dilutes the error-ratio denominator. That alone is not enough, because `/readyz` pings Postgres and Redis through instrumented clients that create a span each with no opt-out; filtering only the HTTP span leaves those as **parentless roots**, noisier than before. `health.Readiness` therefore pings under an unsampled parent span context, which the parentbased_* samplers propagate as a drop. `TestProbesAreNotTraced` and `TestReadinessPingsAreNotSampled` cover the two halves.

**Observability is wired end-to-end:** otelhttp wraps the router, otelpgx traces queries, redisotel traces cache calls; traces and metrics both leave over OTLP/gRPC to the collector; handlers/services start their own spans; slog lines auto-carry `trace_id`/`span_id` via the logger's trace-aware handler. Follow this pattern in new code (tracer per package, `WarnContext`/`ErrorContext` with ctx).

**Metrics come from libraries, never hand-rolled**, so a new resource inherits them without writing any: otelhttp emits the RED signals, `otelpgx.RecordStats` the pgx pool gauges, `redisotel.InstrumentMetrics` the Redis client pool, and `contrib/instrumentation/runtime` the `go.*` runtime metrics. That last one is started explicitly in `telemetry.Setup` because pushing has no client_golang registry to contribute a Go collector by default - drop it and goroutine count and heap size stop existing. Four consequences:
- The `routePattern` middleware is load-bearing for metrics, not just span names: otelhttp wraps the router from outside and never sees chi's pattern, so it pushes `http.route` into otelhttp's Labeler post-routing. Delete it and every endpoint collapses into one latency histogram. It skips unmatched paths on purpose - labelling a 404 with the raw URL is unbounded cardinality from unauthenticated input. `TestRequestMetricsCarryRoutePattern` covers both halves.
- `telemetry.Setup` must run **before** `postgres.NewPool` and `redis.New` - both capture `otel.GetMeterProvider()` at call time, so a pool built first is instrumented against the no-op provider and silently exports nothing. `app.Run` already orders it that way; keep it.
- Before adding an instrument, check it is not derivable from `http_server_*`. A per-endpoint success counter is not - it is `http_server_request_duration_seconds_count` filtered by status. Build any genuinely new instrument once in the constructor, never per request, and keep its attributes to a bounded set: one label carrying an id, email or `trace_id` is one time series per value and takes down the collector, not just the dashboard.
- `instance` comes from `service.instance.id`, stamped from the hostname in `telemetry.Setup`. Scraping derived it from the target address for free; pushing, only the process can say who it is, and without it every replica collapses onto one series. `deploy/prometheus.yml` must keep `honor_labels: true` or the scrape overwrites both `job` and `instance` with the collector's identity.

Build provenance reaches Prometheus as `target_info{service_version,vcs_ref_head_revision}` via `telemetry.WithBuild(version, commit)` - the OTel resource, not a separate `build_info` gauge, because the resource already flows to traces too. `deploy/rules.yml` holds the recording + alerting rules. **Its series names were verified against a real export, not guessed** - instrument names rarely match intuition (`pgxpool.acquire_duration` scrapes as `pgxpool_acquire_duration_nanoseconds_total`; redisotel emits `db_client_connections_*`, not `redis_*`). Re-verify after any instrumentation change and run `promtool check rules deploy/rules.yml`. An alert querying a series that does not exist never fires and never tells you.

## Conventions

- Comments explain *why* or a contract, never *what*. Every exported symbol has a doc comment (revive enforces this).
- **A service package is named after its use case, and its name is resource-qualified, not package-relative.** Each `internal/service/<name>` package exports `<Name>Service` (interface) and `New<Name>Service` (constructor), and keeps the implementation unexported as `<name>Service`:

```go
package auth

type AuthService interface { ... }
type authService struct {
    repo repository.Store // only because this service owns transactions
    ...
}
func NewAuthService(repo repository.Store, log *slog.Logger) AuthService { return &authService{...} }
func (s *authService) Register(ctx context.Context, input RegisterInput) (RegisterOutput, error)
```

  Call sites read `authservice.NewAuthService(repo, log)` and `Deps{Auth: authservice.AuthService}`. The name repeats what the package already says, which is the point: at the composition layer (`di.go`, `server.go`) and in `Deps` fields, half a dozen imports are aliased `*service` and a bare `Service` says nothing about which one. The receiver name follows the same rule so a stack trace or a grep for `authService` finds one thing.

  Note what the name does *not* promise: `<name>` is the use case the package serves, not the table it writes. `authService` owns four tables and there is no rule that a table gets a service, or that a service gets only one table. A package appears when an endpoint group needs one - a table on its own is not a reason to create one.
- **Constructor parameters tell you whether a service owns transactions.** A service that runs an `ExecTx` takes `repository.Store` and stores it; a service that only ever runs inside somebody else's transaction takes no repository at all and receives a `Querier` per method. Reading the constructor in `di.go` should be enough to know which kind you are looking at.

- **No `var _ Service = (*svc)(nil)` assertions in service packages.** The constructor returns the interface, so the compiler already rejects an incomplete implementation at the `return &userService{...}` line. Adding the assertion only matters if a constructor returns the concrete type - which this codebase does not do.
- **A service package is exactly two files: `model.go` (types) and `service.go` (interface + implementation).** There is no `input.go`/`output.go` split - one method's parameter and return type are read together far more often than all the inputs are read together. Split further only past ~200 lines.
- **Types in `model.go` are suffixed `Input` or `Output`, named after the method they belong to**: `Create` takes `CreateInput` and returns `CreateOutput`; `Issue` takes `IssueInput` and returns `IssueOutput`. Types nested inside an Output carry the suffix too (`RegisterOutput.User` is a `UserOutput`), so a bare name in a service package always means something that is not part of a method's signature - `jwt.Claims` is the current example. Note the consequence: when a resource grows `Get` and `List`, `GetOutput` may be field-for-field identical to `CreateOutput`. Accept the duplication until a *third* identical copy appears; collapsing early is what couples two endpoints that were free to diverge.
- **Service types carry no struct tags.** `json` and `validate` tags live on `handler/<resource>`'s `Request`/`Response` types, `pgtype` on sqlc's generated rows. Three shapes, three reasons: the wire contract in `docs/04-api-contract.md` must stay stable while the domain moves, gRPC arrives in M3 as a second consumer, and the snapshot pattern is nested in JSON but flat in the database (`docs/05-schema.md`). The handler maps between them (`toRegisterResponse` in `handler/auth/response.go`) - a service must never build a response type.
- Import order: stdlib / third-party / `github.com/disillusioned-labs/audit/...` last, grouped by `gofumpt.module-path` and `goimports.local-prefixes` in `.golangci.yml`.
- Line endings are LF everywhere (`.gitattributes` enforces); CRLF breaks gofumpt.
- Tests use hand-written fakes (`fakeStore`, `fakeQuerier`, `stubAuthService` patterns) - no mock generation tooling. The flat graph makes these small: a service test fakes `repository.Querier` and nothing else, because there is no other collaborator to fake.
- `internal/repository/integration_test.go` is the template integration test: testcontainers Postgres + real goose migrations + full CRUD.
