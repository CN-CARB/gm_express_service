# Express Service
<p align="left">
    <a href="https://discord.gg/5JUqZjzmYJ" alt="Discord Invite"><img src="https://img.shields.io/discord/981394195812085770?label=Support&logo=discord&logoColor=white" /></a>
</p>

This is the backend web project that supports the [GMod Express Addon](https://github.com/cfc-Servers/gm_express).

This repository now ships the self-hosted Go service only; the former Cloudflare Worker deployment has been retired.

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
| `GM_EXPRESS_EXPIRATION` | `300` | Data TTL in seconds |
| `GM_EXPRESS_MAX_ENTRIES` | `1024` | Max stored payloads before eviction |
| `GM_EXPRESS_MAX_BYTES` | `536870912` | Max retained payload bytes |

Storage is in-memory; restarting the process drops all data and tokens.

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
