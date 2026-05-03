/**
 * One-time script to generate Polymarket CLOB API credentials.
 * Run with: POLYMARKET_PRIVATE_KEY=0x... node scripts/gen-api-keys.mjs
 */

import { ClobClient } from "@polymarket/clob-client-v2";
import { createWalletClient, http } from "viem";
import { privateKeyToAccount } from "viem/accounts";
import { polygon } from "viem/chains";

const PRIVATE_KEY = process.env.POLYMARKET_PRIVATE_KEY;

if (!PRIVATE_KEY) {
  console.error("Error: POLYMARKET_PRIVATE_KEY environment variable is not set.");
  console.error("Usage: POLYMARKET_PRIVATE_KEY=0x... node scripts/gen-api-keys.mjs");
  process.exit(1);
}

if (!PRIVATE_KEY.startsWith("0x") || PRIVATE_KEY.length !== 66) {
  console.error("Error: Private key must start with 0x and be 66 characters total.");
  process.exit(1);
}

console.log("Connecting to Polymarket CLOB...");

const account = privateKeyToAccount(PRIVATE_KEY);
const signer = createWalletClient({ account, chain: polygon, transport: http() });
const client = new ClobClient({ host: "https://clob.polymarket.com", chain: 137, signer });

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
console.log("\nDone. Save these now — the secret and passphrase cannot be retrieved again without your private key + nonce.");
