# Local bandwidth-capped comparison (10 and 200 Mbps)

This directory compares the actual Go+LRU service with the original self-hosted
Deno/JS service, then models their addons' cache paths in a Python client. It is
**not a GMod engine benchmark**: net/Protobuf transport, game-frame scheduling,
Lua hashing/filesystem costs, serialization, and TLS are excluded. No throughput
claim here isolates language/runtime alone.

## Reproduce

From the Go worktree root, with the JS worktree at `../gm_express_service`:

```sh
docker build -f bench/10mbps/Dockerfile.go -t gm-express-bench-go:local .
docker build -f "$PWD/bench/10mbps/Dockerfile.js" -t gm-express-bench-js:local ../gm_express_service
docker build -f bench/10mbps/Dockerfile.client -t gm-express-bench-client:local bench/10mbps
python3 bench/10mbps/orchestrate.py
```

The orchestrator creates its own Docker bridge and containers, runs 10 Mbps then
200 Mbps, and removes those containers and network in `finally`. Existing names
cause an error rather than reuse/removal of someone else's resources. Images and
result files remain for reproducibility. **Host network qdiscs are not changed.**

## Limits and workloads

- Linux `tc tbf` on each container's `eth0`: `10mbit`, then `200mbit`, burst `16kb`,
  latency/queue budget `500ms`. `500ms` is not an injected propagation delay.
- Client upload egress and service download egress are independently capped.
  The cap is **aggregate per direction**, shared by four workers when concurrent;
  it is not a separate 10/200 Mbps allowance for every request. TCP ACKs and
  headers also consume link budget. Client and server `tc -s -j` counters are saved.
- The services have identical limits: 1 CPU, 1 GiB memory, data/token TTL 86400s.
  Go retains its 512 MiB payload budget; both services use their native entry limits.
  The workload fits below capacity, so it does not intentionally induce eviction.
  LRU behavior is validated separately by Go unit/race tests.
- Client: 2 CPUs, 1 GiB. No host ports are published; direct HTTP over Docker bridge.
- Payloads: 100 KiB = 102400 B, 1 MiB = 1048576 B, 10 MiB = 10485760 B.
  They all exceed NetLFS's 64000-byte threshold, so **all three use HTTP**.
- New random bytes per cold sample, generated outside timing. No compression.
  Payload bytes are identical for paired Go/JS trials. Backend order alternates.
- Sequential: three repetitions of each size and scenario per backend.
- Concurrent: two batches of four independent cold upload+download transfers
  per size/backend. A barrier starts all four workers together. This is not
  one-upload broadcast; batch wall time and each worker's latency are recorded.
- Initialization, registration, payload generation and a startup warmup are not timed.
  Each flow uses a persistent HTTP connection. Payload hashes and full byte
  equality are verified; any HTTP or integrity failure aborts the benchmark.

## Cache scenarios and timings

1. `cold`: sender hashes and uploads, receiver downloads and hashes; NetLFS also
   writes a local file. Both strategies skip `/size` with no download filter.
2. `sender_cached`: upload ID is reused; receiver downloads. This models another
   receiver without a local copy. The NetLFS path deliberately does not consult
   the file left by the cold sample.
3. `receiver_cached`: same recipient receives the data again. NetLFS reads and
   verifies its local file, making **zero HTTP requests**; Express downloads again.

The model uses SHA256 for NetLFS and SHA1 for Express. Hashing is implemented by
Python/OpenSSL, **not GMod util.SHA256/SHA1**. Disk writes are buffered (no fsync);
repeat reads can hit the OS page cache. The cache mount uses a host temporary
directory: on the measured machine `/tmp` is **tmpfs**, so these are RAM-backed
file operations, not physical SSD/HDD measurements. Hashing and local-cache
numbers therefore must not be presented as measured GMod frame costs or cold-disk
latency.

`total_s` includes the modeled addon operations and Express's actual configured
notification wait of 150 ms (`EXPRESS_DELAY=0.15`). `http_s` is upload+download HTTP
request time only, excluding that wait and hashing/file writes; use it for the
fairer service comparison. A zero-delay Express deployment can avoid that fixed
wait, but it still has no built-in receive cache. Game net notifications and ACK
latency are not simulated.

## Artifacts

`results/*-samples.jsonl` contains every transfer and verification result;
`*-summary.json` contains medians; `*-metadata.json` records workload settings and
client qdiscs; `*-server-qdisc-*.json` records server limits/counters. `images.json`
records exact image identities. Container statistics are end-of-run snapshots,
**not peak RSS or steady-state resource measurements**.

Small sample counts are suitable for a reproducible local comparison, not claims
about p95/p99, maximum RPS, Internet latency or statistically significant small
percentage differences. TCP shaping and default qdisc burst allowance affect
short-transfer rates, particularly at 200 Mbps.

To verify the current NetLFS optional-size control flow against the actual Lua:

```sh
luajit bench/10mbps/net_lfs_optional_size.lua ../gm_net_lfs/lua/net_lfs/sh_main.lua
```
