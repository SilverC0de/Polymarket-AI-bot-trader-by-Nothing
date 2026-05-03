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

### 2. Generate CLOB API credentials from your private key

Polymarket's CLOB API requires three credentials (`API_KEY`, `API_SECRET`, `API_PASSPHRASE`) derived from your **EOA private key**. Run the generation script once to produce them:

```bash
cd scripts
npm install
cd ..

POLYMARKET_PRIVATE_KEY=0x<your-64-hex-private-key> node scripts/gen-api-keys.mjs
```

The script will print three values:

```
POLYMARKET_API_KEY=...
POLYMARKET_API_SECRET=...
POLYMARKET_API_PASSPHRASE=...
```

Copy these into your `.env` file.

> **Security:** Your private key never leaves your machine — the script signs a local credential-derivation request and only outputs the derived API credentials.

### 3. Set your proxy wallet

Your `POLYMARKET_PROXY_WALLET` is the address shown on your Polymarket profile page (the on-chain proxy that holds your funds). Add it to `.env`.

### 4. Set the signature type

| Value | When to use |
|-------|------------|
| `1` | POLY_PROXY — Magic, email, or Google login |
| `2` | GNOSIS_SAFE — Browser wallet (MetaMask, etc.) |

Set `POLYMARKET_SIG_TYPE` accordingly in `.env`.

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
| `POLYMARKET_PROXY_WALLET` | Live trading | On-chain proxy wallet address (`0x…`) |
| `POLYMARKET_SIG_TYPE` | Live trading | `1` = POLY_PROXY, `2` = GNOSIS_SAFE |
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
| `GET` | `/finance/history/{page}` | Paginated event history (200 events per page). |

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
- **Infrastructure hint from that page:** Polymarket lists **primary servers** in `eu-west-2` and the **closest non-georestricted region** as `eu-west-1`. Deploying in an **allowed** EU-adjacent region (for example Fly.io `ams`, `mad`, or `dub` — not `lhr`, `fra`, or `cdg`, since GB/FR/DE are restricted — always cross-check the current list) often aligns with that guidance and can reduce latency to their stack.

This repo’s [`fly.toml`](fly.toml) sets `primary_region = "jnb"` (Johannesburg) as an example; South Africa is not on the blocked-country table in the article above, but restrictions change — confirm against Polymarket’s page before production and set `primary_region` to whatever region your host provides that remains compliant.

---

## Development

```bash
# Run tests
go test ./...

# Vet
go vet ./...
```

Go dependencies are managed via `go.mod`. Node dependencies (scripts only) are in `scripts/package.json`.
