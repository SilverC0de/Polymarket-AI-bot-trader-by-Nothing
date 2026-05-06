# Polymarket BTC Bot

An automated trading bot for Polymarket's **BTC 5-minute** binary markets. The bot discovers active rounds, streams the live BTC price, evaluates a directional strategy (timing windows, momentum, trend, and overextension filters), and either simulates trades or places real CLOB orders.

---

## How it works

1. **Market discovery** — polls the Polymarket Gamma API to find open BTC 5-minute rounds.
2. **Price streaming** — subscribes to a WebSocket price feed for real-time BTC data.
3. **Strategy evaluation** — on each tick the engine checks timing, price distance, momentum, trend, and overextension conditions to determine a directional signal.
4. **Order execution** — in live mode, places a CLOB order through Polymarket's API with your proxy wallet.
5. **Event persistence** — trades, skips, and outcomes are stored in memory or Redis.

---

## Project structure

```
.
├── cmd/
│   ├── api/            # HTTP server + embedded simulator (primary binary)
│   └── simulator/      # Standalone CLI simulator (TTY only, no live orders)
├── internal/
│   ├── handler/        # HTTP handlers (/finance, /finance/history)
│   ├── server/         # Router, middleware, security headers
│   ├── service/        # SimulatorService — orchestrates sim, price feed, live trading
│   ├── simulator/      # Strategy, engine, market discovery
│   └── store/          # Event log (in-memory or Redis)
├── pkg/
│   └── polymarket/     # Gamma API client, CLOB client, WebSocket price feed, trader
├── scripts/
│   ├── gen-api-keys.mjs  # Derives CLOB API credentials from a private key
│   └── package.json
├── .env.example
└── go.mod
```

---

## Prerequisites

| Tool | Version |
|------|---------|
| Go | 1.22+ |
| Node.js | 18+ (for key generation script only) |
| Redis | Optional — only needed for persistent event history |

---

## Setup

### 1. Clone and configure environment

```bash
git clone <repo-url>
cd Polymarket-Bot
cp .env.example .env
```

Open `.env` and fill in the values described below.

### 2. Generate CLOB API credentials (`scripts/gen-api-keys.mjs`)

Polymarket’s CLOB expects three values in `.env`: `POLYMARKET_API_KEY`, `POLYMARKET_API_SECRET`, and `POLYMARKET_API_PASSPHRASE`. You generate them once from your **EOA private key** (the key that signs orders). Install script dependencies first:

```bash
cd scripts
npm install
cd ..
```

How you run the script depends on **how you use Polymarket**:

| Account style | `POLYMARKET_SIG_TYPE` | When generating keys | `POLYMARKET_PROXY_WALLET` in `.env` |
|---------------|-------------------------|----------------------|-------------------------------------|
| **Email / Magic / Google** (Polymarket “social” login) | `1` (POLY_PROXY) | Pass **proxy wallet + sig type** so the key is registered for your deposit/proxy address | Profile address where your balance lives (`polymarket.com/profile`) |
| **Browser wallet** linked to Polymarket (MetaMask, Rabby, etc.) | `2` (GNOSIS_SAFE) | Same as above: **proxy wallet + sig type** | Same — the proxy shown on your profile |
| **Standalone EOA** (you trade from the same address as your signing key, no Polymarket proxy) | `0` (EOA) | Private key only — **do not** set `POLYMARKET_PROXY_WALLET` or `POLYMARKET_SIG_TYPE` for the script | Set to your **EOA address** (the `0x…` derived from `POLYMARKET_PRIVATE_KEY`; it must match the maker the API expects) |

**Typical Polymarket.com users (email or browser wallet)** — register the API key against your **proxy (funder)** address so it lines up with live trading:

```bash
POLYMARKET_PRIVATE_KEY=0x<your-64-hex-private-key> \
POLYMARKET_PROXY_WALLET=0x<proxy-from-profile> \
POLYMARKET_SIG_TYPE=1 \
node scripts/gen-api-keys.mjs
```

Use `POLYMARKET_SIG_TYPE=2` instead of `1` if you use a browser wallet as in the table above.

**Pure EOA path** (advanced; only if your Polymarket setup is non-proxy):

```bash
POLYMARKET_PRIVATE_KEY=0x<your-64-hex-private-key> node scripts/gen-api-keys.mjs
```

The script prints three lines to paste into `.env` (replace any previous API credentials if you switched accounts or regenerated keys).

> **Security:** Your private key never leaves your machine — the script signs Polymarket’s credential-derivation flow locally and only prints the derived API key, secret, and passphrase. Do **not** commit `.env` or paste real keys into the README, issues, or chat logs.

**Consistency checklist**

- **`POLYMARKET_SIG_TYPE` in `.env`** must match how you signed up (see table). The live trader sends L2 auth using your **EOA** for type `0`, and your **proxy wallet address** for types `1` and `2`, which must match how the API key was registered when you ran the script.
- After **creating a new Polymarket account** or changing login method, regenerate API credentials and update all three `POLYMARKET_API_*` variables. Old keys are tied to the previous registration.
- If orders fail with **maker address not allowed** or **deposit wallet** messaging, you usually have a mismatch: proxy wallet / signature type / API key registration / or `POLYMARKET_PRIVATE_KEY` from a different wallet than the one tied to that Polymarket account.

### 3. Set proxy wallet and signature type in `.env`

Fill `POLYMARKET_PROXY_WALLET` from your profile when you use signature types `1` or `2`. For type `0`, set it to your EOA address as in the table. Set `POLYMARKET_SIG_TYPE` to `0`, `1`, or `2` to match your account.

---

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `PORT` | No | HTTP port for `cmd/api` (default: `8080`; `.env.example` uses `8088`) |
| `LIVE_TRADING` | No | Set to `true` to place real orders; omit or set to anything else for simulation-only mode |
| `POLYMARKET_PRIVATE_KEY` | Live trading | EOA private key (`0x` + 64 hex chars). Used for signing orders and for key generation. |
| `POLYMARKET_API_KEY` | Live trading | CLOB API key (from `gen-api-keys.mjs`) |
| `POLYMARKET_API_SECRET` | Live trading | CLOB API secret (from `gen-api-keys.mjs`) |
| `POLYMARKET_API_PASSPHRASE` | Live trading | CLOB API passphrase (from `gen-api-keys.mjs`) |
| `POLYMARKET_PROXY_WALLET` | Live trading | Proxy wallet from profile (`0x…`) for types `1`–`2`; for type `0`, use your EOA address (same as the address of `POLYMARKET_PRIVATE_KEY`). |
| `POLYMARKET_SIG_TYPE` | Live trading | `0` = EOA (standalone signer). `1` = POLY_PROXY (email / Magic / Google). `2` = GNOSIS_SAFE (browser wallet proxy). Must match how you generated API keys and how you use Polymarket. |
| `REDIS_URL` | No | Redis connection string. If empty, event log is in-memory only. |

---

## Running

### API server (recommended)

Runs the HTTP server with the embedded simulator. Supports live trading and Redis persistence.

```bash
go run ./cmd/api
```

The server reads `.env` from the current working directory automatically.

**Endpoints:**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/finance` | Current simulator status and latest events. Accepts optional `?history_limit=N`. |
| `GET` | `/finance/history/{page}` | Paginated event history (10 events per page). |

### CLI simulator

Runs the simulator in your terminal with a live price display. Does **not** place real orders regardless of `LIVE_TRADING`.

```bash
go run ./cmd/simulator
```

If the WebSocket connection fails, the simulator automatically falls back to demo mode.

### Live trading mode

Set `LIVE_TRADING=true` in `.env` and ensure all `POLYMARKET_*` credentials are populated, then run the API server:

```bash
go run ./cmd/api
```

The `SimulatorService` will wire up `polymarket.Trader` and submit real CLOB orders when the strategy fires.

---

## Redis (optional)

If `REDIS_URL` is set, the API server persists all events to Redis so history survives restarts. Without it, the event log lives in memory only.

```env
REDIS_URL=redis://localhost:6379
```

---

## Deployment region

Polymarket blocks or limits access from many countries and some subregions for compliance reasons. Your bot’s **egress IP** (where the host runs) may be treated like a user location, so pick a provider region that is allowed before you enable live trading.

- **Official list and rules:** [Geographic restrictions](https://help.polymarket.com/en/articles/13364163-geographic-restrictions) — includes fully blocked countries (e.g. US, GB, DE, FR), close-only markets, and blocked subregions (e.g. Ontario). Polymarket also states that using VPNs or similar to bypass restrictions violates their Terms of Service.
- **Infrastructure hint from that page:** Polymarket lists **primary servers** in `eu-west-2` and the **closest non-georestricted region** as `eu-west-1`. Deploying in an **allowed** EU-adjacent [Fly.io region](https://fly.io/docs/reference/regions/) (e.g. `ams` in the Netherlands, or `arn` in Sweden — not `lhr`, `fra`, or `cdg` if you want to avoid GB/DE/FR egress; always cross-check the current Polymarket list) often aligns with that guidance and can reduce latency to their stack.

This repo’s [`fly.toml`](fly.toml) sets `primary_region = "ams"` (Amsterdam). After changing region, recreate or migrate Machines if needed (`fly scale count`, `fly machines list`, or Fly’s [machine placement](https://fly.io/docs/machines/guides-examples/machine-placement/) docs). Restrictions change over time — confirm against Polymarket’s page before production.

---

## Development

```bash
# Run tests
go test ./...

# Vet
go vet ./...
```

Go dependencies are managed via `go.mod`. Node dependencies (scripts only) are in `scripts/package.json`.
