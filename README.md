# Express Service
<p align="left">
    <a href="https://discord.gg/5JUqZjzmYJ" alt="Discord Invite"><img src="https://img.shields.io/discord/981394195812085770?label=Support&logo=discord&logoColor=white" /></a>
</p>

This is the backend web project that supports the [GMod Express Addon](https://github.com/cfc-Servers/gm_express).

This repository now ships the self-hosted Go service only; the former Cloudflare Worker deployment has been retired.

## Protocol reference

[Go HTTP protocol and client integration (中文)](docs/protocol.md). The service root (`GET /`) redirects to [NetLFS](https://github.com/CN-CARB/NetLFS); use `/v1/revision` for health checks.

## Deploy your own

The included `docker-compose.yml` builds and runs the Go service:

```bash
docker compose up --build -d
```

By default, Express listens on port 3000. Change host binding or published port through `API_HOST` and `API_PORT` in `.env`.

To build without Docker (requires [Go 1.22+](https://go.dev/dl/)):
```bash
cd goserver
go build -o express-go .
./express-go
```

Configuration via environment variables:
| Variable | Default | Purpose |
|---|---|---|
| `API_HOST` | `0.0.0.0` | Address to bind |
| `API_PORT` | `3000` | Port to bind |
| `GM_EXPRESS_EXPIRATION` | `86400` | Data TTL in seconds (24 hours) |
| `GM_EXPRESS_MAX_ENTRIES` | `1024` | Max stored payloads before eviction |
| `GM_EXPRESS_MAX_BYTES` | `536870912` | Max retained payload bytes |

Data and tokens default to a 24-hour lifetime. `GM_EXPRESS_EXPIRATION` overrides only the data TTL; token lifetime remains 24 hours, and `/v1/register` reports the token lifetime as `expiration: 86400`. Capacity eviction can remove entries earlier.

Storage is in-memory; restarting the process drops all data and tokens.

Both payloads and tokens use LRU eviction: successful reads and writes promote the entry to most recently used. Reads do not extend TTL. When the entry limit or payload-byte budget is reached, expired entries are reclaimed first, then the least recently used live entries are evicted until the new value fits. A single payload larger than the entire byte budget is rejected without evicting existing entries. Payload and size metadata are one entry and expire or evict together. The byte budget counts retained payload bytes, not total process memory.

Downloads use Go's `http.ServeContent` for HTTP byte ranges, including suffix and open-ended ranges. Successful ranges return `206` with matching byte boundaries. Non-overlapping ranges for a nonempty file return `416` with `Content-Range: bytes */<size>`; an empty file is returned as an empty `200` response. Multipart requests follow the Go runtime's range-count policy: with current Go releases and this module's Go 1.22 compatibility setting, multiple ranges are ignored and the full file is returned with `200`. The service does not override that policy.

## Validation

Run `go test -race ./...` and `go vet ./...` from `goserver/`. Tests cover LRU ordering, count/byte eviction, fixed TTL, failed oversized writes, concurrent access, HTTP compatibility, the NetLFS redirect and Range behavior.

[10 / 200 Mbps benchmark methodology](bench/10mbps/README.md) and [measured results](bench/10mbps/RESULTS.md) include raw samples and reproducible Docker tooling. They measure real HTTP services with modeled client cache flows, not in-engine GMod performance.

---

### Configuring the addon for self-hosting
If you host your own Express instance, you'll need to change a couple of convars.

<br>

#### **`express_domain`**
This convar tells both the Server _and_ Client what domain they can find Express at. By default, it's `gmod.express` - the public & free Express instance.

Set this to your self-hosted Express domain.

<br>

#### **`express_domain_cl`**
This convar lets you set a specific domains for Clients. If you leave it empty, both Server and Client will use `express_domain`.

This convar is useful if you self-host Express on the same machine that runs your Garry's Mod server. In that setup, you'll want to do something like this:
```
# Tell the server to find Express locally
# (me.cfc.gg redirects to 127.0.0.1 to get around Gmod's localhost HTTP restrictions)
express_domain "me.cfc.gg:3000"

# Tell the clients to find it at your server's public IP (or, ideally, HTTPS-ready Domain)
express_domain_cl "23.227.174.90:3000"
```
