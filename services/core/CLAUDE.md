# services/core — owner B

Go core API. Contracts: `contracts/openapi.yaml`, `contracts/events.md`. Schema: migrations in `migrations/`, queries via sqlc into `internal/gen/` (excluded from coverage).
- Every handler checks the JWT and role; data of other users is never returned (R71).
- Actions: confirm endpoint issues an HMAC confirmation token; `actions` row is written on MCP result and linked to the plan task (R66); `idempotency_key` unique (R79).
- SSE: relay agent events unchanged; persist messages; on agent timeout emit `error{retryable:true}` and close cleanly.
- Feature flags: runtime in `feature_flags` + Redis cache; compile-time via build tag `demo`.
- Events: outbox table → Kafka in profile `full`; direct insert into `analytics_events` in profile `core`.
- Tests: `go test ./... -race -cover`; integration tests use testcontainers or the compose Postgres.
