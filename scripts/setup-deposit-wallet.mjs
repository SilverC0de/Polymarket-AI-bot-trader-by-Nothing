/**
 * One-time setup script: deploy a Polymarket deposit wallet and approve trading contracts.
 *
 * Get a Relayer API Key from: https://polymarket.com/settings?tab=api-keys
 *
 * Usage:
 *   POLYMARKET_PRIVATE_KEY=0x... \
 *   POLYMARKET_API_KEY=... \
 *   POLYMARKET_API_SECRET=... \
 *   POLYMARKET_API_PASSPHRASE=... \
 *   node scripts/setup-deposit-wallet.mjs
 *
 * After running:
 *   1. Set POLYMARKET_PROXY_WALLET=<printed address> in .env
 *   2. Transfer pUSD to that deposit wallet address (NOT your EOA)
 *   3. Run gen-api-keys.mjs with POLYMARKET_PROXY_WALLET + POLYMARKET_SIG_TYPE=3
 *   4. Set POLYMARKET_SIG_TYPE=3 and LIVE_TRADING=true in .env
 */

import { createWalletClient, encodeFunctionData, http, maxUint256 } from "viem";
import { privateKeyToAccount } from "viem/accounts";
import { polygon } from "viem/chains";
import { RelayClient } from "@polymarket/builder-relayer-client";
import { BuilderConfig } from "@polymarket/builder-signing-sdk";

const PRIVATE_KEY = process.env.POLYMARKET_PRIVATE_KEY;
const API_KEY     = process.env.POLYMARKET_API_KEY;
const API_SECRET  = process.env.POLYMARKET_API_SECRET;
const API_PASS    = process.env.POLYMARKET_API_PASSPHRASE;
const RELAYER_URL = process.env.RELAYER_URL ?? "https://relayer-v2.polymarket.com/";

// Polygon mainnet contract addresses
const PUSD_ADDRESS        = "0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB";
const CTF_ADDRESS         = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045";
const EXCHANGE_V2         = "0xE111180000d2663C0091e4f400237545B87B996B";
const NEG_RISK_EXCHANGE_V2 = "0xe2222d279d744050d28e00520010520000310F59";
const NEG_RISK_ADAPTER    = "0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296";

if (!PRIVATE_KEY) {
  console.error("Error: POLYMARKET_PRIVATE_KEY is not set.");
  console.error("\nUsage:");
  console.error("  POLYMARKET_PRIVATE_KEY=0x... POLYMARKET_API_KEY=... POLYMARKET_API_SECRET=... POLYMARKET_API_PASSPHRASE=... node scripts/setup-deposit-wallet.mjs");
  process.exit(1);
}

const account = privateKeyToAccount(PRIVATE_KEY);
const walletClient = createWalletClient({ account, chain: polygon, transport: http() });
console.log(`EOA address : ${account.address}`);
console.log(`Relayer URL : ${RELAYER_URL}`);
console.log("");

// Build optional HMAC builder auth (uses same key/secret/passphrase as CLOB API).
// If credentials aren't set the client still works for address derivation (local).
let builderConfig;
if (API_KEY && API_SECRET && API_PASS) {
  builderConfig = new BuilderConfig({
    localBuilderCreds: { key: API_KEY, secret: API_SECRET, passphrase: API_PASS },
  });
  console.log("Builder auth configured.");
} else {
  console.log("No builder auth (POLYMARKET_API_KEY/SECRET/PASSPHRASE not set). Will attempt unauthenticated relayer call.");
}

// RelayClient@0.0.9 uses positional constructor: (url, chainId, signer, builderConfig?, txType?)
const relayer = new RelayClient(RELAYER_URL, 137, walletClient, builderConfig);

// Step 1: Derive deposit wallet address (local, no network needed)
let depositWalletAddress;
try {
  depositWalletAddress = await relayer.deriveDepositWalletAddress();
  console.log(`Deposit wallet address (deterministic): ${depositWalletAddress}`);
} catch (err) {
  console.error("Failed to derive deposit wallet address:", err.message ?? err);
  process.exit(1);
}

// Step 2: Deploy the deposit wallet via relayer
console.log("\nDeploying deposit wallet via relayer WALLET-CREATE...");
try {
  const deployResp = await relayer.deployDepositWallet();
  console.log("Waiting for transaction to be confirmed...");
  const result = await deployResp.wait();
  console.log("Deploy result:", JSON.stringify(result, null, 2));
} catch (err) {
  // The wallet may already be deployed — check for that
  const msg = err.message ?? String(err);
  if (msg.includes("already") || msg.includes("deployed") || msg.includes("DEPLOYED")) {
    console.log("Deposit wallet already deployed — skipping.");
  } else {
    console.error("Deploy failed:", msg);
    console.log("\nYou may need to deploy manually via the Polymarket web app,");
    console.log("or ensure you have a valid builder API key. Continuing with approvals...");
  }
}

// Step 3: Approve trading contracts from the deposit wallet
console.log("\nApproving pUSD spending for trading contracts from deposit wallet...");

const spenders = [
  { address: EXCHANGE_V2,          label: "Exchange V2" },
  { address: NEG_RISK_EXCHANGE_V2, label: "NegRisk Exchange V2" },
  { address: NEG_RISK_ADAPTER,     label: "NegRisk Adapter" },
  { address: CTF_ADDRESS,          label: "CTF (conditional tokens)" },
];

for (const { address: spender, label } of spenders) {
  const approveData = encodeFunctionData({
    abi: [{
      name: "approve", type: "function",
      inputs: [{ name: "spender", type: "address" }, { name: "amount", type: "uint256" }],
      outputs: [{ type: "bool" }],
    }],
    functionName: "approve",
    args: [spender, maxUint256],
  });

  const deadline = Math.floor(Date.now() / 1000 + 300).toString();
  try {
    const resp = await relayer.executeDepositWalletBatch(
      [{ target: PUSD_ADDRESS, value: "0", data: approveData }],
      depositWalletAddress,
      deadline,
    );
    const result = await resp.wait();
    console.log(`  ✓ Approved pUSD for ${label}:`, result?.state ?? "done");
  } catch (err) {
    console.warn(`  ⚠ Approval for ${label} failed: ${err.message ?? err}`);
  }
}

// Done
console.log("\n══════════════════════════════════════════════════════");
console.log("Setup complete!");
console.log("══════════════════════════════════════════════════════\n");
console.log("Next steps:\n");
console.log(`  1. Add to .env:`);
console.log(`       POLYMARKET_PROXY_WALLET=${depositWalletAddress}`);
console.log(`       POLYMARKET_SIG_TYPE=3`);
console.log(`  2. Transfer pUSD to the deposit wallet (NOT to your EOA):`);
console.log(`       ${depositWalletAddress}`);
console.log(`  3. Generate new API keys for the deposit wallet:`);
console.log(`       POLYMARKET_PRIVATE_KEY=... POLYMARKET_PROXY_WALLET=${depositWalletAddress} POLYMARKET_SIG_TYPE=3 node scripts/gen-api-keys.mjs`);
console.log(`  4. Update POLYMARKET_API_KEY/SECRET/PASSPHRASE in .env with the new keys`);
console.log(`  5. Enable live trading: LIVE_TRADING=true`);
console.log("\npUSD on the EOA does NOT count as CLOB buying power — it must be in the deposit wallet.");
