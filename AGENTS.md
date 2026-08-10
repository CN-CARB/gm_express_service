# PROJECT KNOWLEDGE BASE

**Generated:** 2026-08-10
**Commit:** fe40a77
**Branch:** main

## OVERVIEW

Self-hosted backend for GMod Express addon. Single dependency-free Go HTTP service with in-memory, TTL-bound payload and token stores; Docker/GHCR deployment.

## STRUCTURE

```text
.
├── goserver/                 # Entire application and test suite
│   ├── main.go               # HTTP API, routing, config, process entry point
│   ├── store.go              # Concurrent TTL/capacity/memory-bounded store
│   └── main_test.go          # HTTP compatibility and store behavior
├── docker/Dockerfile         # Static scratch image
├── docker-compose.yml        # Self-hosted runtime wiring
└── .github/workflows/ci.yml  # Test, binary build, image publish
```

No active Cloudflare Worker code remains. Ignore `.omo/`; agent runtime state, not product source.

## WHERE TO LOOK

| Task | Location | Notes |
|---|---|---|
| Add/change endpoint | `goserver/main.go` | Routes registered in `newMux` with Go 1.22 method patterns |
| Preserve addon protocol | `goserver/main_test.go` | Legacy status/body/range quirks are executable contract |
| Change retention/eviction | `goserver/store.go` | One lock protects map and `usedBytes` accounting |
| Change limits/defaults | `goserver/main.go`, `.env`, `docker-compose.yml` | Keep code, sample env, Compose defaults aligned |
| Change server hardening | `goserver/main.go:newServer` | Transfer-friendly read/write timeouts are intentional |
| Change container build | `docker/Dockerfile` | Build context is repository root |
| Change CI/release | `.github/workflows/ci.yml` | Pushes SHA image; default branch also pushes `latest` |

## CODE MAP

| Symbol | Type | Location | Refs | Role |
|---|---|---|---:|---|
| `main` | function | `goserver/main.go:201` | 3 | Loads bind config, creates stores, starts server |
| `newMux` | function | `goserver/main.go:148` | 2 | API composition root; also installs active stores |
| `NewStore` | function | `goserver/store.go:23` | 11 | Creates bounded store and janitor goroutine |
| `Store.Set` | method | `goserver/store.go:52` | 13 | Capacity, byte-budget, expiry, random eviction |
| `Store.Get` | method | `goserver/store.go:29` | 6 | Read plus lazy expired-entry deletion |
| `handleWrite` | handler | `goserver/main.go:83` | 1 | Auth, 24 MiB body limit, payload insertion |
| `handleRead` | handler | `goserver/main.go:105` | 1 | Auth, lookup, legacy byte-range response |
| `parseRange` | function | `goserver/main.go:169` | 1 | Legacy-compatible range interpretation |
| `newServer` | function | `goserver/main.go:189` | 2 | HTTP timeout/header bounds |

## CONVENTIONS

- Run Go commands from `goserver/`; module root is nested.
- Standard library only. `go.mod` has no third-party dependencies.
- Tests exercise handlers through `httptest`, not a live socket.
- HTTP response bodies are compatibility-sensitive, including punctuation, empty bodies, JSON strings, and unconventional range endpoints.
- Configuration accepts positive integers only; missing, invalid, zero, or negative values fall back to compiled defaults.
- Payload bytes and size metadata stay together in one `Store`; eviction must remove both behaviorally.
- `newMux` assigns package-global stores. Do not make handler tests parallel without first removing that shared state.

## ANTI-PATTERNS (THIS PROJECT)

- Do not normalize legacy responses as cleanup. `{"size":"10"}`, missing-size `{}`, and exact error text are client contracts.
- Do not replace current range behavior with RFC interpretation unless addon compatibility and tests change together.
- Do not read oversized known-length request bodies; reject from `Content-Length` first.
- Do not add LRU bookkeeping speculatively. Random eviction is deliberate; production evidence must justify added state.
- Do not add persistent/external storage without changing documented restart semantics.
- Do not restore retired Worker deployment paths.

## COMMANDS

```bash
cd goserver
go test ./...
go build -o express-go .

# Repository root
docker build --file docker/Dockerfile --tag gm-express-service:local .
docker compose up --build
```

CI cross-builds with:

```bash
cd goserver
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o express-go .
```

## NOTES

- Storage and registration tokens are process-local; restart invalidates both.
- Data TTL defaults to 300 seconds; token TTL is fixed at 24 hours.
- Payload hard limit is 24 MiB, independent from retained-byte budget.
- Store janitor scans every 30 seconds; `Get` and capacity pressure also remove expired entries.
- Docker runtime is `scratch`, runs UID/GID `65532`, and exposes port 3000.
- CI runs only when service, Docker, Compose, or workflow paths change.
