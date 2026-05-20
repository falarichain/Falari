# FalariChain

FalariChain is a **decentralized storage chain purpose‑built for the AI era**. It combines POS + BFT consensus with verifiable storage deals, retrieval mining, and on‑chain governance — while providing an **Agent Key system** inspired by API‑Key workflows that lets AI agents, training pipelines, and automated services interact with the chain securely, without exposing master account credentials.

---

## Why AI Agents Love FalariChain

| Pain Point | Existing Solutions | FalariChain |
|-----------|-------------------|-------------|
| **Agent needs a private key on server** | Risk of full fund loss if compromised | Agent Key: no transfer/delegate/governance permissions, immutable spending caps |
| **Multiple agents sharing one account** | Shared nonce = sequential transactions, bottlenecks | Each Agent Key has its **own Nonce counter** — fully parallel |
| **Tracking which agent spent what** | Manual off‑chain accounting | Every transaction carries `agent_key_id` — auditable on‑chain |
| **Rotating or revoking credentials** | Manual key redistribution | One command: `chainctl agent-key revoke` — instant, master key untouched |
| **Uploading 100 GB+ datasets** | Single‑shot HTTP, no resume | Streaming upload with resume token — Gateway handles erasure coding and parallel miner dispatch |

**The result**: an AI training pipeline can own its Agent Key, pay from the organization's master account, stay within a daily budget, and never put the master wallet at risk — all while block explorers and billing dashboards show a unified view under the same master address.

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Core Concepts](#core-concepts)
3. [Quick Start](#quick-start)
4. [Deployment Guide](#deployment-guide)
   - [Chain Node](#chain-node)
   - [Storage Node (Miner)](#storage-node-miner)
   - [Retrieval Node](#retrieval-node)
   - [Indexer](#indexer)
5. [CLI Reference (chainctl)](#cli-reference-chainctl)
6. [HTTP API Reference](#http-api-reference)
   - [Chain Node](#chain-node-api)
   - [Storage / Retrieval Node](#storage--retrieval-node-api)
7. [Configuration Reference](#configuration-reference)
8. [Maintenance & Operations](#maintenance--operations)
9. [Upgrade Guide](#upgrade-guide)
10. [Troubleshooting](#troubleshooting)

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                        FalariChain Network                           │
│                                                                      │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌───────────────┐    │
│  │ Chain    │   │ Chain    │   │ Chain    │   │   Indexer     │    │
│  │ Node 1   │◄──│ Node 2   │◄──│ Node N   │   │  (read-only)  │    │
│  │(proposer)│   │(validator)│  │(validator)│   └───────┬───────┘    │
│  └────┬─────┘   └──────────┘   └──────────┘           │            │
│       │  libp2p + GossipSub                              │            │
│       │                                                  │            │
│  ┌────┴─────────────────────────────────────────────────┴──────┐    │
│  │                     Storage Layer                            │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │    │
│  │  │ Storage Node │  │ Storage Node │  │ Storage Node │      │    │
│  │  │    (Miner)   │  │    (Miner)   │  │    (Miner)   │      │    │
│  │  └──────────────┘  └──────────────┘  └──────────────┘      │    │
│  │                                                              │    │
│  │  ┌──────────────┐  ┌──────────────┐                         │    │
│  │  │  Retrieval   │  │  Retrieval   │   ← retrieval mining   │    │
│  │  │    Node      │  │    Node      │                         │    │
│  │  └──────────────┘  └──────────────┘                         │    │
│  └─────────────────────────────────────────────────────────────┘    │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                    AI Agents & Applications                   │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │   │
│  │  │ Training │  │Inference │  │  Backup  │  │ RAG Data │   │   │
│  │  │ Pipeline │  │ Service  │  │   Bot    │  │ Ingestion│   │   │
│  │  │ (key_abc)│  │ (key_def)│  │ (key_ghi)│  │ (key_jkl)│   │   │
│  │  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘   │   │
│  │       └──────────────┴────────────┴──────────────┘          │   │
│  │                          │                                   │   │
│  │     Agent Key (ECDSA)    │    All from: 0xa1b2c3d4...        │   │
│  │     Daily limits + Permissions  │  Unified billing view      │   │
│  └─────────────────────────────────┴───────────────────────────┘   │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                       Users / dApps                           │   │
│  │                 chainctl CLI  │  HTTP API  │  libp2p          │   │
│  └──────────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────┘
```

- **Chain Nodes** run POS + BFT consensus, produce blocks, execute transactions, and maintain the on‑chain state.
- **Storage Nodes (Miners)** store file shards, submit storage proofs, and earn storage rewards.
- **Retrieval Nodes** serve file downloads, earn retrieval mining rewards, and can optionally act as an **upload gateway**（erasure coding, miner dispatch, batch commit）— this gives retrieval node operators a business incentive to offer upload services, since more data on the chain means more retrieval requests and more rewards.
- **Indexer** syncs blocks from a chain node and provides search APIs for deals, CIDs, and providers.
- **AI Agents** use Agent Keys（API‑Key‑style credentials）to upload datasets, download checkpoints, and manage collections — with immutable spending limits, fine‑grained permissions, and zero risk to the master wallet.

---

## Core Concepts

### Token Economy

| Parameter | Value |
|-----------|-------|
| Total Supply | 10,000,000,000 |
| Storage Pool | 6,300,000,000 (63%) |
| Retrieval Pool | 1,200,000,000 (12%) |
| Validator Pool | 1,000,000,000 (10%) |
| Repair Pool | 500,000,000 (5%) |

- Pools release tokens each epoch at a configurable BPS rate（default: storage 3 / retrieval 20 / validator 2 per 10,000 per epoch）.
- Storage / retrieval / repair rewards are paid from user‑locked fees first, falling back to the corresponding pool.

### POS + BFT Consensus

- Validators are selected by **stake‑weighted random proposer election**（SHA‑256 seed from height + round + prev block hash）.
- Multi‑round BFT with up to 5 rounds per height. If a proposer times out, the next proposer takes over.
- A block is finalized when ≥ 2/3 of total voting power has voted.

### Storage Deals

1. **Create Intent** — User defines file metadata, erasure coding policy, segment plan, and storage policy（duration, price, etc.）.
2. **Assign Providers** — Chain assigns storage providers（miners）based on health score, capacity, and anti‑spam reputation.
3. **Upload** — Client erasure‑encodes the file and uploads shards to assigned miners.
4. **Finalize** — After upload, the deal becomes active. Miners periodically submit storage proofs.
5. **Renew / Expire** — Users can renew deals. Expired deals enter a 7‑day grace period, after which they are settled and providers are released.

### Deletion Policy

| Policy | Behavior |
|--------|----------|
| `standard` | Grace period → expire → refund → data released |
| `retain_evidence` | Keep data but suspend access. No refund. No delete tasks. |
| `immediate` | Settle immediately on expiry（skip grace period）. Normal delete tasks. |

### Retrieval Mining

- Retrieval Nodes serve file downloads and collect signed receipts.
- Receipts submitted on‑chain earn rewards from user‑locked fees or the retrieval pool.
- **Anti‑spam**: hard cap of 100 rewards per user/intent/hour. Speed‑sampling to detect abuse. Anti‑spam score weighs into provider health.

### Governance

- Committee operations: `freeze`（suspend access），`block`（schedule deletion + preserve evidence），`legal_hold`（preserve data），`appeal`.
- All governance actions produce on‑chain audit records.

### Agent Keys（AI‑Friendly Delegated Access）

Agent Keys let users create **API‑Key‑style credentials** for AI agents, training pipelines, and automated services — without exposing the master private key.

**How it works:**

- The **master account** creates an Agent Key with a spending budget, daily limit, expiry time, and operation whitelist.
- Each Agent Key has its **own ECDSA key pair and its own Nonce counter** — multiple agents can transact concurrently without blocking each other.
- The agent signs its transactions with the Agent Key private key, but the **`from` field in every block is always the master address** — so block explorers and billing reports show a unified view.
- The agent **never holds the master private key**, and even if the agent server is compromised, the attacker cannot transfer funds, change governance, or withdraw stake.

**Three immutable limits（set at creation，cannot be changed）:**

| Limit | Description |
|-------|-------------|
| `daily_limit` | Maximum tokens spendable per calendar day |
| `total_limit` | Lifetime spending cap（key becomes unusable once reached） |
| `expires_at` | Unix timestamp after which the key is auto‑revoked（0 = no expiry） |

**Allowed operations（whitelist only）:**

| Operation | Allowed | Reason |
|-----------|---------|--------|
| `create_intent` / `batch_commit` / `finalize` | ✅ | Data upload |
| `renew` | ✅ | Deal renewal |
| `retrieval` | ✅ | Data download |
| `collection_create` / `append_record` | ✅ | Dataset management |
| `transfer` / `faucet` / `delegate` / `terminate_deal` | ❌ | Financial / destructive |
| `access_policy` / `governance` / `register_validator` | ❌ | Administrative |

**Security & transparency:**

- The **master address** and **agent public key** are stored on‑chain（required for consensus verification）.
- The **agent private key is never sent to the chain** — it is generated locally and stored in a file.
- User can check all their keys from any wallet：`chainctl agent-key list -master 0xa1b2...`
- Every transaction includes the `agent_key_id` field, so users can trace which agent signed each transaction.
- Both register and revoke requests require an **ECDSA signature from the master private key** with a unique Nonce for replay protection.

---

## Quick Start

### Prerequisites

- **Go** ≥ 1.24
- **OS**: Linux / macOS / WSL2
- **Ports**: 8080（chain），9090（storage），9091（retrieval），9095（indexer）

### 1. Clone & Build

```bash
git clone <repo-url> chain
cd chain
go build ./...
```

### 2. Start a Single‑Node Devnet

**Terminal 1 — Chain Node**

```bash
go run ./cmd/chainnode/ \
  -addr :8080 \
  -state ./data/chain.json \
  -block-interval 5s \
  -epoch-interval 30s \
  -epoch-duration 25s \
  -settle-interval 60s \
  -validator-key ./data/validator.json
```

**Terminal 2 — Storage Miner**

```bash
go run ./cmd/storagenode/ \
  -addr :9090 \
  -data ./data/miner1 \
  -chain http://localhost:8080 \
  -capacity 1073741824 \
  -stake 1000 \
  -faucet \
  -auto-prove
```

**Terminal 3 — Retrieval Node**（optional）

```bash
go run ./cmd/retrievalnode/ \
  -addr :9091 \
  -data ./data/retrieval1 \
  -chain http://localhost:8080 \
  -auto-collect
```

### 3. Upload a File

```bash
# Create an intent
go run ./cmd/chainctl/ create-intent \
  -user alice \
  -file ./example.txt \
  -duration 86400

# Upload shards
go run ./cmd/chainctl/ upload \
  -intent <intent-id> \
  -storage http://localhost:9090

# Finalize the deal
go run ./cmd/chainctl/ finalize -intent <intent-id>
```

### 4. Download a File

```bash
go run ./cmd/chainctl/ download \
  -intent <intent-id> \
  -output ./restored.txt
```

### 5. Create an Agent Key (one string, paste to AI agent)

```bash
go run ./cmd/chainctl/ agent-key create \
  -name "my-training-bot" \
  -allow "create_intent,batch_commit,finalize" \
  -daily-limit 500 \
  -total-limit 10000 \
  -expire 90d \
  -master-key ./my-org.key
```

Outputs a single string like `fara_a2V5X01IUW1TS...` — **copy and paste this to your AI agent**. The agent needs nothing else.

```bash
# List all keys under a master address
go run ./cmd/chainctl/ agent-key list -master 0xa1b2c3...

# Revoke a compromised key
go run ./cmd/chainctl/ agent-key revoke -id key_abc123 -master-key ./my-org.key
```

**Agent side** (any language, ~5 lines):
```python
import base64
raw = base64.urlsafe_b64decode(key_string[5:] + "==").decode()
agent_key_id, master, address, private_key = raw.split("|")
# use private_key to sign, from=master, agent_key_id in tx
```

### 6. Check Status

```bash
go run ./cmd/chainctl/ status
go run ./cmd/chainctl/ consensus
```

---

## Deployment Guide

### Chain Node

The chain node is the consensus participant. In production, run **at least 4 nodes** for BFT safety.

```bash
chainnode \
  -addr :8080                          # HTTP listen address
  -state /data/chain/state.json        # State file（LevelDB when using .db extension）
  -block-interval 5s                   # Block production interval（0 to disable）
  -epoch-interval 10m                  # Epoch interval（0 to disable）
  -epoch-duration 9m                   # Epoch duration（must be < interval）
  -epoch-challenges 3                  # Challenges per finalized deal per epoch
  -epoch-reward 1                      # Reward per accepted proof
  -epoch-slash 1                       # Slash per missed proof
  -settle-interval 1m                  # Intent settlement interval（0 to disable）
  -validator-key /data/chain/validator.json   # Validator identity
  -validator-endpoint https://node.example.com:8080
  -validator-stake 100000              # Stake from validator's own balance
  -peers https://peer1:8080,https://peer2:8080   # Peer chain nodes
  -p2p-listen /ip4/0.0.0.0/tcp/4001    # libp2p listen address
  -p2p-peers /ip4/peer1/tcp/4001/p2p/<id>
  -p2p-topic storage-chain/devnet
  -sync-interval 5s                    # Peer block sync interval
```

**State persistence**: When `-state` ends with `.db`, the node uses LevelDB; otherwise it uses JSON file storage. LevelDB is recommended for production.

### Storage Node (Miner)

```bash
storagenode \
  -addr :9090
  -data /data/storage1
  -chain https://chain.example.com:8080
  -endpoint https://miner1.example.com:9090
  -capacity 1099511627776        # 1 TiB
  -stake 100000
  -auto-prove                    # Auto submit storage proofs
  -prove-interval 5s
  -auto-repair                   # Auto execute repair tasks
  -repair-interval 30s
  -auto-delete                   # Auto execute delete tasks
  -delete-interval 30s
  -p2p-listen /ip4/0.0.0.0/tcp/4002,/ip4/0.0.0.0/udp/4002/quic-v1
  -p2p-topic storage-chain/providers/devnet
```

### Retrieval Node

```bash
retrievalnode \
  -addr :9091
  -data /data/retrieval1
  -chain https://chain.example.com:8080
  -endpoint https://retrieval1.example.com:9091
  -capacity 1099511627776
  -stake 50000
  -auto-collect                  # Auto sign and submit retrieval receipts
  -collect-interval 30s
  -cache-size 1024               # In-memory shard cache entries
  -p2p-listen /ip4/0.0.0.0/tcp/4003,/ip4/0.0.0.0/udp/4003/quic-v1
```

**Differences from Storage Node**: Retrieval nodes focus on downloads and receipt collection. They do not run proof or repair loops.

### Indexer

```bash
indexer \
  -addr :9095
  -chain https://chain.example.com:8080
  -sync-interval 5s
```

**API endpoints**:
- `GET /status` — Indexing statistics（blocks indexed, deals indexed, CIDs indexed, last sync time）
- `GET /search/deals?q=<query>` — Full‑text search across intent IDs, file names, and user addresses
- `GET /search/cids?q=<query>` — CID → intent + miner mapping

---

## CLI Reference (chainctl)

```
chainctl <command> [flags]

Core Commands:
  create-intent       Create a storage intent
  upload              Upload shards to storage nodes
  finalize            Finalize an upload and create a deal
  download            Download / reconstruct a file
  status              Show chain and storage node status
  manifest            Export the on‑chain manifest

Deal Lifecycle:
  settle-intent       Settle a storage intent
  renew               Renew a deal
  terminate-deal      Terminate a deal
  delete-tasks        List pending delete tasks
  access-policy       Set access policy（public/private/suspended/blocked）

Governance:
  governance-deal     Apply governance action（freeze/block/legal_hold/appeal）
  committee-freeze-deal   Committee freeze a deal
  governance-block-deal   Governance block a deal
  governance-audit    List governance audit records

Data Collections:
  collection-create   Create a data collection
  collection-append   Append a record to a collection
  collection-records  List collection records
  record              Show a single record

Mining & Proofs:
  repair              Repair missing shards
  prove               Submit a storage proof
  epoch               Manually start a proof epoch
  finalize-epoch      Manually finalize an epoch
  request-retrieval   Request file retrieval
  retrieval-receipt   Submit a retrieval receipt

Consensus & Network:
  consensus           View consensus state
  set-upgrade         Set an upgrade plan
  produce-block       Manually produce a block
  vote-block          Vote on a block
  validators          List validators
  peers               List peer nodes
  storage-providers   Query storage providers

Accounts:
  account-new         Generate a new account key pair
  balance             Check account balance
  transfer            Transfer tokens
  faucet              Request dev faucet funds
  intent              View intent details
  miner               View miner stats
  agent-key           Manage AI agent keys（create / list / revoke）

Blockchain:
  block               View a block by height
  mempool             View the transaction mempool
```

Common flags for most commands: `-chain`（default `http://localhost:8080`），`-key`（account key file path）.

---

## HTTP API Reference

### Chain Node API

All endpoints are served from the chain node's `-addr`（default `:8080`）.

#### Status & Consensus

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/status` | Full chain status（height, pools, miners, validators, etc.） |
| `GET` | `/snapshot` | Complete state snapshot |
| `GET` | `/consensus` | Consensus engine state（height, round, phase, proposer） |
| `GET` | `/upgrade` | Current upgrade plan |
| `POST` | `/upgrade` | Set upgrade plan（`{"name","halt_height","halt_time","info"}`） |

#### Storage Intents

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/intents` | Create a storage intent |
| `GET` | `/intents/` | Get intent by ID（path suffix） |
| `POST` | `/intents/settle` | Settle an expired intent |
| `POST` | `/intents/{id}/renew` | Renew a deal |
| `POST` | `/intents/terminate` | Terminate a deal |
| `POST` | `/intents/access` | Set access policy |
| `POST` | `/intents/governance` | Apply governance action |
| `POST` | `/intents/governance/freeze` | Committee freeze |
| `POST` | `/intents/governance/block` | Governance block |
| `GET` | `/intents/delete-tasks` | List delete tasks（`?status=pending`） |
| `GET` | `/intents/governance/audit` | List governance audit records |
| `GET` | `/intents/health` | All deal health statuses |
| `GET` | `/intents/{id}/health` | Single deal health status |
| `GET` | `/manifests/` | Get storage manifest（by intent ID or deal ID） |

#### Storage Providers & Proofs

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/storage/quote` | Get a storage price quote |
| `GET` | `/storage/providers` | List providers（`?shard_hash=` / `?shard_cid=` / `?intent_id=`） |
| `POST` | `/storage/providers` | Accept a provider announcement |
| `POST` | `/batch-commits` | Batch commit upload receipts |
| `POST` | `/finalize` | Finalize an upload |
| `POST` | `/challenges` | Generate storage challenges |
| `GET` | `/challenges` | List challenges（`?pending=true&miner=`） |
| `POST` | `/proofs` | Submit a storage proof |
| `GET` | `/repairs` | Get repair plan（`?intent=`） |
| `POST` | `/repairs` | Create repair tasks |
| `POST` | `/delete-receipts` | Submit a delete receipt |

#### Retrieval

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/retrieval-receipts` | Submit a retrieval receipt |
| `GET` | `/retrieval-receipts` | List retrieval receipts |

#### Epochs

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/epochs` | Start a proof epoch |
| `POST` | `/epochs/finalize` | Finalize an epoch |
| `GET` | `/epochs/{id}/rewards` | Get epoch reward breakdown |

#### Miners & Validators

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/miners` | Register a miner |
| `POST` | `/miners/deregister` | Deregister a miner |
| `GET` | `/miners/` | Get miner stats（`?address=`） |
| `POST` | `/validators` | Register a validator |
| `GET` | `/validators` | List validators |
| `POST` | `/validators/deregister` | Deregister a validator |
| `POST` | `/validators/delegate` | Delegate stake |
| `POST` | `/validators/undelegate` | Undelegate stake |
| `POST` | `/validators/evidence` | Submit validator misbehavior evidence |

#### Accounts & Transactions

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/faucet` | Dev faucet（`{"address","amount"}`） |
| `POST` | `/transfer` | Transfer tokens |
| `POST` | `/tx/raw` | Submit raw transaction（deprecated） |
| `GET` | `/accounts/` | Get account（`?address=` or path suffix） |

#### Agent Key Management

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/agent-keys` | Register a new Agent Key（master‑signed：`{"master","name","agent_pub","permissions","daily_limit","total_limit","expires_at","signature"}`） |
| `GET` | `/agent-keys` | List all Agent Keys for a master address（`?master=0x...`） |
| `POST` | `/agent-keys/revoke` | Revoke an Agent Key（master‑signed：`{"key_id","master","nonce","signature"}`） |

#### Blocks & Mempool

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/mempool` | Get mempool（pending txs + fee market） |
| `POST` | `/blocks/produce` | Manually produce a block |
| `POST` | `/blocks/votes` | Submit a block vote |
| `POST` | `/consensus/votes` | Submit a consensus vote |
| `GET` | `/blocks/latest` | Get latest block |
| `GET` | `/blocks/` | Get block by height（path suffix） |

#### P2P

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/p2p/blocks` | Receive a block from a peer |
| `POST` | `/p2p/txs` | Receive a transaction from a peer |
| `GET` | `/peers` | List connected peers |

#### Data Collections

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/collections` | Create a data collection |
| `GET` | `/collections/` | Get collection（`?id=` or path suffix） |
| `GET` | `/collections/{id}/records` | List records（`?collection=` `?type=` `?latest=` `?limit=`） |
| `POST` | `/records` | Append a record |
| `GET` | `/records/{id}/manifest` | Get record manifest |
| `GET` | `/records/` | Get a single record |

---

### Storage / Retrieval Node API

Served from the node's `-addr`（default storage `:9090`, retrieval `:9091`）.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/status` | Node status（address, peer ID, transport counters, stored shards） |
| `GET` | `/identity` | Node identity（address + public key） |
| `GET` | `/providers` | Query providers（`?shard_hash=` or `/providers/{shard_hash}`） |
| `POST` | `/upload` | Upload a shard（`{"shard_hash","shard_cid","shard_size","data_base64"}`） |
| `POST` | `/prove` | Request a storage proof for a challenge |
| `POST` | `/retrieval-receipts/sign` | Sign a retrieval receipt |
| `GET` | `/blocks/{cid}` | Download a shard by CID |
| `GET` | `/shards/{hash}.bin` | Download a shard by hash |

---

## Configuration Reference

### Token Pool Release Rates

Controlled by constants in `internal/reward/types.go`:

| Pool | Default Rate (BPS) | Per Epoch |
|------|-------------------|-----------|
| Storage | 3 / 10,000 | 0.03% |
| Retrieval | 20 / 10,000 | 0.20% |
| Validator | 2 / 10,000 | 0.02% |

### Storage Pricing

Controlled by constants in `internal/chain/state.go`:

| Parameter | Default |
|-----------|---------|
| Base price per GiB/month | 1 |
| Minimum fee | 1 |
| Duration for "permanent" storage | 10 years（315,360,000 seconds） |

### Fee Market

| Parameter | Default |
|-----------|---------|
| Base fee | 1 |
| Target block transactions | 10 |
| Adjustment step | base_fee / 8（per block） |

### Miner Scoring

| Weight | Default (BPS) |
|--------|---------------|
| Stored bytes | 4,000 |
| Proof success score | 3,500 |
| Availability | 1,500 |
| Decentralization | 1,000 |
| Anti‑spam score bonus | up to 1,000 |
| Speed score bonus | up to 1,000 |

### Grace Period & Deletion

| Parameter | Value |
|-----------|-------|
| Grace period after expiry | 7 days |
| Max retrieval reward per window | 100 |
| Retrieval rate window | 1 hour |
| Speed sample window | 60 seconds |
| Abuse detection threshold | avg > 10x normal |

---

## Maintenance & Operations

### Health Monitoring

```bash
# Chain node health
chainctl status
chainctl consensus

# Deal health（at‑risk / critical status）
curl http://localhost:8080/intents/health

# Storage node health
curl http://localhost:9090/status
```

### Managing Deals

```bash
# Renew a deal before expiry
chainctl renew -intent <id> -duration 86400 -user alice

# Terminate a deal
chainctl terminate-deal -intent <id> -user alice

# View pending delete tasks
chainctl delete-tasks
```

### Governance Operations

```bash
# Freeze a deal（suspends access）
chainctl governance-deal -intent <id> -operator admin -action freeze -reason "DMCA"

# Block a deal（schedules deletion）
chainctl governance-deal -intent <id> -operator admin -action block -reason "illegal"

# Legal hold（preserve data, block access）
chainctl governance-deal -intent <id> -operator admin -action legal_hold

# View audit trail
chainctl governance-audit
```

### Validator Management

```bash
# List validators
chainctl validators

# Delegate stake
chainctl delegate -validator <addr> -amount 5000

# Submit evidence of double‑signing
chainctl evidence -vote-a <vote> -vote-b <vote>
```

### Backup & Recovery

- **Chain state**: Back up the `-state` file or LevelDB directory regularly.
- **Storage data**: Back up the `-data` directory（blocks, hash/CID indexes）.
- **Node identity**: Back up `validator.json` / `node.json` — losing the private key means losing staked funds.

### Logging

All binaries log to stdout. Key log lines to watch:

```
connected libp2p peer ...                     # P2P connection established
accepted gossip block height=N ...            # Block received via GossipSub
accepted gossip tx ...                        # Transaction received via GossipSub
synced block height=N ...                     # Block synced from peer
token release epoch=N storage=X ...           # Epoch reward released
auto prover error: ...                        # Proof submission failure
auto repair error: ...                        # Repair failure
```

---

## Upgrade Guide

### Setting an Upgrade Plan

A chain upgrade is coordinated via the on‑chain `UpgradePlan`:

```bash
chainctl set-upgrade \
  -name "v1.1.0" \
  -halt_height 50000 \
  -info "Enable new storage pricing model"
```

The chain will **automatically halt** at the specified height or time. All validators must upgrade their binaries before the halt point.

Parameters:

| Flag | Description |
|------|-------------|
| `-name` | Upgrade name（required） |
| `-halt_height` | Block height at which to halt（0 to use time only） |
| `-halt_time` | Unix timestamp at which to halt（0 to use height only） |
| `-info` | Human‑readable upgrade description |

### Upgrade Procedure

1. **Announce** the upgrade plan via `chainctl set-upgrade`.
2. **Verify** the plan is on‑chain: `curl http://localhost:8080/upgrade`.
3. **Upgrade binaries** on all nodes before the halt point.
4. **Restart nodes** — they will resume from the halt point with the new protocol version.

---

## Troubleshooting

### Chain node won't produce blocks

- Check the validator is registered: `chainctl validators`
- Check the validator has stake: `chainctl balance -address <validator-addr>`
- Verify `-block-interval` is set and `-validator-key` is loaded
- Check consensus state: `chainctl consensus`
- If `phase=wait`, the chain may be halted for upgrade: `curl http://localhost:8080/upgrade`

### Storage node not receiving proofs

- Verify miner registration: `chainctl miner -address <miner-addr>`
- Check miner status is `active`（not `degraded` / `jailed` / `exiting`）
- Ensure `-chain` points to a reachable chain node
- Check `-auto-prove` is enabled

### Retrieval rewards not being collected

- Ensure `-auto-collect` is enabled
- Check the retrieval node has registered as a miner
- Verify storage providers are reachable（`-p2p-listen` / `-p2p-peers`）

### Peer sync failures

- Check network connectivity between chain nodes
- Verify `-peers` URLs are correct and reachable
- Check `-p2p-listen` addresses are publicly accessible for NAT traversal
- Enable QUIC transport for better NAT penetration: add `/udp/0/quic-v1` to `-p2p-listen`

### Data corruption / state file errors

- For JSON state: the file can be manually inspected with `jq`
- For LevelDB state: use `leveldb` CLI tools or copy the `.db` directory
- Restore from backup and restart the node

---

## Development

### Running Tests

```bash
go test ./...
```

### Project Structure

```
chain/
├── cmd/                    # Executable entry points
│   ├── chainctl/           # CLI client
│   ├── chainnode/          # Chain node（validator / proposer）
│   ├── storagenode/        # Storage miner
│   ├── retrievalnode/      # Retrieval miner
│   └── indexer/            # Read‑only indexer
├── internal/
│   ├── chain/              # Core chain logic（state, block, consensus, lifecycle）
│   ├── client/             # Client library（encryption, erasure coding, HTTP, streaming）
│   ├── crypto/             # SHA‑256 / Merkle tree
│   ├── storage/            # Storage backend（blockstore, transport, proof, provider selection）
│   ├── wire/               # Data types and serialization
│   ├── reward/             # Token reward pool logic
│   ├── consensus/          # Consensus types
│   ├── governance/         # Governance types
│   └── indexer/            # Indexer engine
└── go.mod
```

---

## License

FalariChain is open source software licensed under the [MIT License](LICENSE).
