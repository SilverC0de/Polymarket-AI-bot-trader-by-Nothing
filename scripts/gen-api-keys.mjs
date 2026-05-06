/**
 * One-time script to generate Polymarket CLOB API credentials.
 * Run with: POLYMARKET_PRIVATE_KEY=0x... node scripts/gen-api-keys.mjs
 * 
 * For POLY_PROXY accounts (email/social login), also set:
 *   POLYMARKET_PROXY_WALLET=0x... POLYMARKET_SIG_TYPE=1 node scripts/gen-api-keys.mjs
 */

import { ClobClient } from "@polymarket/clob-client-v2";
import { createWalletClient, http } from "viem";
import { privateKeyToAccount } from "viem/accounts";
import { polygon } from "viem/chains";

const PRIVATE_KEY = process.env.POLYMARKET_PRIVATE_KEY;
const PROXY_WALLET = process.env.POLYMARKET_PROXY_WALLET;
const SIG_TYPE = process.env.POLYMARKET_SIG_TYPE ? parseInt(process.env.POLYMARKET_SIG_TYPE, 10) : 0;

if (!PRIVATE_KEY) {
  console.error("Error: POLYMARKET_PRIVATE_KEY environment variable is not set.");
  console.error("Usage: POLYMARKET_PRIVATE_KEY=0x... node scripts/gen-api-keys.mjs");
  console.error("\nFor POLY_PROXY accounts (email/social login), also set:");
  console.error("  POLYMARKET_PROXY_WALLET=0x... POLYMARKET_SIG_TYPE=1 node scripts/gen-api-keys.mjs");
  process.exit(1);
}

if (!PRIVATE_KEY.startsWith("0x") || PRIVATE_KEY.length !== 66) {
  console.error("Error: Private key must start with 0x and be 66 characters total.");
  process.exit(1);
}

const account = privateKeyToAccount(PRIVATE_KEY);
const signer = createWalletClient({ account, chain: polygon, transport: http() });

// For POLY_PROXY/GNOSIS_SAFE accounts, pass funder (proxy wallet) to register API key correctly
const clientConfig = {
  host: "https://clob.polymarket.com",
  chain: 137,
  signer,
};

if (PROXY_WALLET && (SIG_TYPE === 1 || SIG_TYPE === 2)) {
  console.log(`Registering API key for POLY_PROXY account (proxy: ${PROXY_WALLET})...`);
  clientConfig.funder = PROXY_WALLET;
} else {
  console.log("Registering API key for EOA account...");
}

const client = new ClobClient(clientConfig);

let creds;
try {
  creds = await client.createOrDeriveApiKey();
} catch (err) {
  console.error("Failed to generate credentials:", err.message ?? err);
  process.exit(1);
}

console.log("\n========== Add these to your .env ==========\n");
console.log(`POLYMARKET_API_KEY=${creds.key}`);
console.log(`POLYMARKET_API_SECRET=${creds.secret}`);
console.log(`POLYMARKET_API_PASSPHRASE=${creds.passphrase}`);
console.log("\n============================================");
if (PROXY_WALLET) {
  console.log(`\nAPI key registered for proxy wallet: ${PROXY_WALLET}`);
  console.log(`EOA signer address: ${account.address}`);
}
console.log("\nDone. Save these now — the secret and passphrase cannot be retrieved again without your private key + nonce.");
