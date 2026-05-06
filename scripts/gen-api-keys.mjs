/**
 * One-time script to generate Polymarket CLOB API credentials.
 *
 * Usage:
 *   # Deposit wallet (POLY_1271 — new API users):
 *   POLYMARKET_PRIVATE_KEY=0x... POLYMARKET_DEPOSIT_WALLET=0x... POLYMARKET_SIG_TYPE=3 node scripts/gen-api-keys.mjs
 *
 *   # POLY_PROXY legacy (email/social login):
 *   POLYMARKET_PRIVATE_KEY=0x... POLYMARKET_PROXY_WALLET=0x... POLYMARKET_SIG_TYPE=1 node scripts/gen-api-keys.mjs
 *
 *   # EOA only:
 *   POLYMARKET_PRIVATE_KEY=0x... node scripts/gen-api-keys.mjs
 *
 * Run setup-deposit-wallet.mjs first to deploy and fund your deposit wallet.
 */

import { ClobClient, SignatureTypeV2 } from "@polymarket/clob-client-v2";
import { createWalletClient, http } from "viem";
import { privateKeyToAccount } from "viem/accounts";
import { polygon } from "viem/chains";

const PRIVATE_KEY = process.env.POLYMARKET_PRIVATE_KEY;
const PROXY_WALLET = process.env.POLYMARKET_PROXY_WALLET;
const DEPOSIT_WALLET = process.env.POLYMARKET_DEPOSIT_WALLET;
const SIG_TYPE = process.env.POLYMARKET_SIG_TYPE ? parseInt(process.env.POLYMARKET_SIG_TYPE, 10) : 0;

if (!PRIVATE_KEY) {
  console.error("Error: POLYMARKET_PRIVATE_KEY environment variable is not set.");
  console.error("\nUsage (deposit wallet / POLY_1271):");
  console.error("  POLYMARKET_PRIVATE_KEY=0x... POLYMARKET_DEPOSIT_WALLET=0x... POLYMARKET_SIG_TYPE=3 node scripts/gen-api-keys.mjs");
  process.exit(1);
}

if (!PRIVATE_KEY.startsWith("0x") || PRIVATE_KEY.length !== 66) {
  console.error("Error: Private key must start with 0x and be 66 characters total.");
  process.exit(1);
}

const account = privateKeyToAccount(PRIVATE_KEY);
const signer = createWalletClient({ account, chain: polygon, transport: http() });

const clientConfig = {
  host: "https://clob.polymarket.com",
  chain: 137,
  signer,
};

if (SIG_TYPE === 3) {
  if (!DEPOSIT_WALLET) {
    console.error("Error: POLYMARKET_DEPOSIT_WALLET must be set for POLY_1271 (sig type 3).");
    console.error("Run setup-deposit-wallet.mjs first to deploy your deposit wallet.");
    process.exit(1);
  }
  console.log(`Configuring deposit wallet funder (POLY_1271): ${DEPOSIT_WALLET}`);
  clientConfig.funderAddress = DEPOSIT_WALLET;
  clientConfig.signatureType = SignatureTypeV2.POLY_1271;
} else if (PROXY_WALLET && (SIG_TYPE === 1 || SIG_TYPE === 2)) {
  console.log(`Registering API key for proxy wallet (POLY_PROXY): ${PROXY_WALLET}`);
  clientConfig.funderAddress = PROXY_WALLET;
  clientConfig.signatureType = SIG_TYPE === 2 ? SignatureTypeV2.POLY_GNOSIS_SAFE : SignatureTypeV2.POLY_PROXY;
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
if (SIG_TYPE === 3) {
  console.log(`\nDeposit wallet funder: ${DEPOSIT_WALLET}`);
} else if (PROXY_WALLET) {
  console.log(`\nAPI key registered for proxy wallet: ${PROXY_WALLET}`);
}
console.log(`EOA signer address: ${account.address}`);
console.log("\nDone. Save these now — the secret and passphrase cannot be retrieved again without your private key + nonce.");
