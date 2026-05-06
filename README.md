# Polymarket BTC Bot

An automated trading bot for Polymarket's **BTC 5-minute** binary markets. The bot discovers active rounds, streams the live BTC price, evaluates a directional strategy (timing windows, momentum, trend, and overextension filters), and either simulates trades or places real CLOB orders.

---

## How it works

1. **Market discovery** — polls the Polymarket Gamma API to find open BTC 5-minute rounds.
2. **Price streaming** — subscribes to a WebSocket price feed for real-time BTC data.
3. **Strategy evaluation** — on each tick the engine checks timing, price distance, momentum, trend, and overextension conditions to determine a directional signal.
4. **Order execution** — in live mode, places a CLOB order through Polymarket's API. New API users use a **deposit wallet** as maker/signer with `POLY_1271` (signature type `3`). Legacy setups use an EOA, proxy, or Gnosis Safe path (`0`–`2`).
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
│   ├── gen-api-keys.mjs       # Derives CLOB API credentials from a private key
│   ├── setup-deposit-wallet.mjs  # Optional: relayer deploy + approvals (Node + builder creds)
│   └── package.json
├── .env.example
└── go.mod
```

---

## Prerequisites

| Tool | Version |
|------|---------|
| Go | 1.22+ |
| Node.js | 18+ (for `scripts/*.mjs` only; the Go binary does not embed Node) |
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

### 2. Install script dependencies

```bash
cd scripts
npm install
cd ..
```

### 3. Deposit wallet (new API users — recommended)

Polymarket routes **new** API integrations through a **deposit wallet**: on-chain balance lives there, CLOB orders use `signatureType = 3` (`POLY_1271`), and both `maker` and `signer` are the deposit wallet address. Official guide: [Deposit wallet migration](https://docs.polymarket.com/trading/deposit-wallet-migration).

**Address:** For a given owner EOA, the deposit wallet address is deterministic. It is often the **same** as the “proxy” / profile address you already used on polymarket.com — confirm on your profile or by running `deriveDepositWalletAddress()` from Polymarket’s relayer client.

**In `.env`:**

- `POLYMARKET_SIG_TYPE=3`
- `POLYMARKET_DEPOSIT_WALLET=0x…` — your deposit wallet (must match the address the CLOB expects for your account)
- `POLYMARKET_PROXY_WALLET` — optional for the bot in type `3` mode; you may keep it equal to the deposit address for your own notes

**Generate CLOB API keys** with the owner EOA private key while configuring the deposit wallet funder + `POLY_1271`:

```bash
POLYMARKET_PRIVATE_KEY=0x<your-64-hex-private-key> \
POLYMARKET_DEPOSIT_WALLET=0x<deposit-wallet-from-profile-or-derivation> \
POLYMARKET_SIG_TYPE=3 \
node scripts/gen-api-keys.mjs
```

Paste the printed `POLYMARKET_API_KEY`, `POLYMARKET_API_SECRET`, and `POLYMARKET_API_PASSPHRASE` into `.env`. These credentials are derived by the owner EOA, while live orders still use the deposit wallet as both order `maker` and order `signer`.

**Optional — `scripts/setup-deposit-wallet.mjs`:** One-time relayer steps (deploy `WALLET-CREATE` if needed, then `WALLET` batches for token approvals). The relayer uses **builder** HMAC authentication (see Polymarket’s docs: `BUILDER_API_KEY`, `BUILDER_SECRET`, `BUILDER_PASS_PHRASE`). The script reads the same three strings from `POLYMARKET_API_KEY`, `POLYMARKET_API_SECRET`, and `POLYMARKET_API_PASSPHRASE` **for that run only** — if your CLOB credentials are not registered as builder credentials, pass builder values inline for the script (do not confuse them with CLOB keys unless Polymarket issued you keys that serve both). Usage:

```bash
POLYMARKET_PRIVATE_KEY=0x… \
POLYMARKET_API_KEY=… \
POLYMARKET_API_SECRET=… \
POLYMARKET_API_PASSPHRASE=… \
node scripts/setup-deposit-wallet.mjs
```

If the wallet is already deployed and funded, you can skip this script and rely on the web app for approvals.

**Funding:** Collateral (e.g. USDC.e / pUSD in Polymarket’s flow) must sit in the **deposit wallet** address, not only on the owner EOA, for CLOB buying power. After deposits or allowance changes, sync balances via the CLOB / SDK (`signature_type = 3`).

### 4. Legacy account styles (`gen-api-keys.mjs`)

If you are **not** on the deposit-wallet path, generate keys as follows:

| Account style | `POLYMARKET_SIG_TYPE` | When generating keys | Wallet env in `.env` |
|---------------|------------------------|----------------------|----------------------|
| **Email / Magic / Google** (Polymarket “social” login) | `1` (POLY_PROXY) | Pass **proxy wallet + sig type** | `POLYMARKET_PROXY_WALLET` = profile / funder address |
| **Browser wallet** linked to Polymarket (MetaMask, Rabby, etc.) | `2` (GNOSIS_SAFE) | Same: **proxy wallet + sig type** | Same as profile funder |
| **Standalone EOA** | `0` (EOA) | Private key only | `POLYMARKET_PROXY_WALLET` = EOA address from the same private key |

**POLY_PROXY example:**

```bash
POLYMARKET_PRIVATE_KEY=0x<your-64-hex-private-key> \
POLYMARKET_PROXY_WALLET=0x<proxy-from-profile> \
POLYMARKET_SIG_TYPE=1 \
node scripts/gen-api-keys.mjs
```

Use `POLYMARKET_SIG_TYPE=2` instead of `1` for the browser-wallet / Safe-style path.

**Pure EOA path:**

```bash
POLYMARKET_PRIVATE_KEY=0x<your-64-hex-private-key> node scripts/gen-api-keys.mjs
```

> **Security:** Your private key never leaves your machine — the script signs Polymarket’s credential-derivation flow locally and only prints the derived API key, secret, and passphrase. Do **not** commit `.env` or paste real keys into the README, issues, or chat logs.

**Consistency checklist**

- **`POLYMARKET_SIG_TYPE` in `.env`** must match how you ran `gen-api-keys.mjs`.
- **L2 auth vs order signer:** for type `3`, `POLY_ADDRESS` is the owner EOA used to derive the API key, while the CLOB order itself uses `POLYMARKET_DEPOSIT_WALLET` for both `maker` and `signer`. For types `1` and `2`, `POLY_ADDRESS` is the proxy / funder; for type `0`, it is the EOA.
- After **creating a new Polymarket account** or changing login method, regenerate API credentials and update all three `POLYMARKET_API_*` variables.
- If orders fail with **maker address not allowed** or **deposit wallet** messaging, see the [migration doc](https://docs.polymarket.com/trading/deposit-wallet-migration): use type `3`, set `POLYMARKET_DEPOSIT_WALLET`, regenerate CLOB keys for that funder, and ensure collateral is on the deposit wallet.

---

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `PORT` | No | HTTP port for `cmd/api` (default: `8080`; `.env.example` uses `8088`) |
| `LIVE_TRADING` | No | Set to `true` to place real orders; omit or set to anything else for simulation-only mode |
| `POLYMARKET_PRIVATE_KEY` | Live trading | EOA private key (`0x` + 64 hex chars). Signs orders and the key-derivation flow in `gen-api-keys.mjs`. |
| `POLYMARKET_API_KEY` | Live trading | CLOB API key (from `gen-api-keys.mjs`) |
| `POLYMARKET_API_SECRET` | Live trading | CLOB API secret (from `gen-api-keys.mjs`) |
| `POLYMARKET_API_PASSPHRASE` | Live trading | CLOB API passphrase (from `gen-api-keys.mjs`) |
| `POLYMARKET_SIG_TYPE` | Live trading | `0` = EOA. `1` = POLY_PROXY. `2` = GNOSIS_SAFE. `3` = POLY_1271 / **deposit wallet** (new API users). |
| `POLYMARKET_DEPOSIT_WALLET` | Live trading when `POLYMARKET_SIG_TYPE=3` | `0x…` deposit wallet used as order maker/signer. L2 auth remains tied to the owner EOA. |
| `POLYMARKET_PROXY_WALLET` | Live trading when **not** type `3` | Proxy / funder from profile for types `1`–`2`; for type `0`, your EOA address. Ignored for order placement when using type `3` (deposit wallet replaces maker/signer). |
| `REDIS_URL` | No | Redis connection string. If empty, event log is in-memory only. |

The Go application does **not** need any relayer credentials — the relayer is only used via `setup-deposit-wallet.mjs` (a one-time setup script), which reads `POLYMARKET_API_KEY/SECRET/PASSPHRASE` directly.

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

Set `LIVE_TRADING=true` in `.env` and ensure all required `POLYMARKET_*` credentials are populated, then run the API server:

```bash
go run ./cmd/api
```

The `SimulatorService` wires up `polymarket.Trader` and submits real CLOB orders when the strategy fires.

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
