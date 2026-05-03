# Polymarket BTC Bot

An automated trading bot for Polymarket's **BTC 5-minute** binary markets. The bot discovers active rounds, streams the live BTC price, evaluates a directional strategy (timing windows, momentum, trend, and overextension filters), and either simulates trades or places real CLOB orders.

---

## How it works

1. **Market discovery** — polls the Polymarket Gamma API to find open BTC 5-minute rounds.
2. **Price streaming** — subscribes to a WebSocket price feed for real-time BTC data.
3. **Strategy evaluation** — on each tick the engine checks:
   - The BTC price is between **$40 and $120** away from the round's target.
   - The round ends in **20 seconds to 2 minutes**.
   - No sideways/swing/overextension conditions are active.
   - Momentum and trend signals confirm a directional move.
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

## Strategy defaults

| Parameter | Default |
|-----------|---------|
| Min distance from target | $40 |
| Max distance from target | $120 |
| Entry window (before round end) | 20s – 2m |
| Order size | $10 |
| Trend samples | 5 |
| Momentum samples | 3 (min $0.50/s) |
| Overextension lookback | $12 move in 60s |

---

## Redis (optional)

If `REDIS_URL` is set, the API server persists all events to Redis so history survives restarts. Without it, the event log lives in memory only.

```env
REDIS_URL=redis://localhost:6379
```

---

## Development

```bash
# Run tests
go test ./...

# Vet
go vet ./...
```

Go dependencies are managed via `go.mod`. Node dependencies (scripts only) are in `scripts/package.json`.
