-- Falari Block Explorer Schema
-- PostgreSQL 17+

BEGIN;

-- ============================================================
-- BLOCKS
-- ============================================================
CREATE TABLE IF NOT EXISTS blocks (
    height              BIGINT PRIMARY KEY,
    hash                TEXT NOT NULL,
    prev_hash           TEXT NOT NULL DEFAULT '',
    round               BIGINT NOT NULL DEFAULT 0,
    time_unix           BIGINT NOT NULL,
    tx_root             TEXT NOT NULL DEFAULT '',
    state_root          TEXT NOT NULL DEFAULT '',
    receipts_root       TEXT NOT NULL DEFAULT '',
    producer_address    TEXT NOT NULL,
    producer_public_key TEXT NOT NULL DEFAULT '',
    signature           TEXT NOT NULL DEFAULT '',
    finalized           BOOLEAN NOT NULL DEFAULT FALSE,
    voting_power        BIGINT NOT NULL DEFAULT 0,
    total_power         BIGINT NOT NULL DEFAULT 0,
    tx_count            INT NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_blocks_hash ON blocks(hash);
CREATE INDEX IF NOT EXISTS idx_blocks_time ON blocks(time_unix DESC);
CREATE INDEX IF NOT EXISTS idx_blocks_producer ON blocks(producer_address);

-- ============================================================
-- TRANSACTIONS
-- ============================================================
CREATE TABLE IF NOT EXISTS transactions (
    tx_id               TEXT PRIMARY KEY,
    block_height        BIGINT NOT NULL REFERENCES blocks(height),
    tx_type             TEXT NOT NULL,
    from_address        TEXT NOT NULL DEFAULT '',
    nonce               BIGINT NOT NULL DEFAULT 0,
    nonce_protected     BOOLEAN NOT NULL DEFAULT FALSE,
    agent_key_id        TEXT NOT NULL DEFAULT '',
    agent_nonce         BIGINT NOT NULL DEFAULT 0,
    fee                 BIGINT NOT NULL DEFAULT 0,
    payload_hash        TEXT NOT NULL DEFAULT '',
    payload             JSONB NOT NULL DEFAULT '{}',
    created_at_unix     BIGINT NOT NULL DEFAULT 0,
    indexed_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_txs_block ON transactions(block_height);
CREATE INDEX IF NOT EXISTS idx_txs_type ON transactions(tx_type);
CREATE INDEX IF NOT EXISTS idx_txs_from ON transactions(from_address);
CREATE INDEX IF NOT EXISTS idx_txs_created ON transactions(created_at_unix DESC);

-- Payload 内部字段索引
CREATE INDEX IF NOT EXISTS idx_txs_transfer_to ON transactions ((payload->>'to')) WHERE tx_type = 'transfer';
CREATE INDEX IF NOT EXISTS idx_txs_transfer_amount ON transactions (((payload->>'amount')::BIGINT)) WHERE tx_type = 'transfer';
CREATE INDEX IF NOT EXISTS idx_txs_intent_user ON transactions ((payload->>'user')) WHERE tx_type = 'create_intent';
CREATE INDEX IF NOT EXISTS idx_txs_intent_file ON transactions ((payload->>'file_name')) WHERE tx_type = 'create_intent';
CREATE INDEX IF NOT EXISTS idx_txs_validator_addr ON transactions ((payload->>'address')) WHERE tx_type = 'register_validator';
CREATE INDEX IF NOT EXISTS idx_txs_miner_addr ON transactions ((payload->>'miner_address')) WHERE tx_type = 'register_miner';
CREATE INDEX IF NOT EXISTS idx_txs_intent_id ON transactions ((payload->>'intent_id')) WHERE tx_type IN ('batch_commit', 'finalize', 'create_intent');

-- ============================================================
-- ACCOUNTS (snapshot)
-- ============================================================
CREATE TABLE IF NOT EXISTS accounts (
    address             TEXT PRIMARY KEY,
    public_key          TEXT NOT NULL DEFAULT '',
    balance             BIGINT NOT NULL DEFAULT 0,
    nonce               BIGINT NOT NULL DEFAULT 0,
    locked_stake        BIGINT NOT NULL DEFAULT 0,
    locked_storage      BIGINT NOT NULL DEFAULT 0,
    first_seen_height   BIGINT NOT NULL DEFAULT 0,
    last_updated_height BIGINT NOT NULL DEFAULT 0,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_accounts_balance ON accounts(balance DESC);

-- ============================================================
-- INTENTS (Storage Deals)
-- ============================================================
CREATE TABLE IF NOT EXISTS intents (
    intent_id               TEXT PRIMARY KEY,
    user_address            TEXT NOT NULL,
    file_name               TEXT NOT NULL DEFAULT '',
    file_size               BIGINT NOT NULL DEFAULT 0,
    segment_size            BIGINT NOT NULL DEFAULT 0,
    file_root               TEXT NOT NULL DEFAULT '',
    deal_id                 TEXT NOT NULL DEFAULT '',
    manifest_root           TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL DEFAULT 'uploading',
    storage_status          TEXT NOT NULL DEFAULT 'pending',
    access_status           TEXT NOT NULL DEFAULT 'public',
    moderation_status       TEXT NOT NULL DEFAULT 'none',
    locked_fee              BIGINT NOT NULL DEFAULT 0,
    paid_fee                BIGINT NOT NULL DEFAULT 0,
    refunded_fee            BIGINT NOT NULL DEFAULT 0,
    uploaded_size           BIGINT NOT NULL DEFAULT 0,
    committed_segments      INT NOT NULL DEFAULT 0,
    data_shards             INT NOT NULL DEFAULT 0,
    parity_shards           INT NOT NULL DEFAULT 0,
    shard_size              INT NOT NULL DEFAULT 0,
    policy_class            TEXT NOT NULL DEFAULT '',
    policy_duration         BIGINT NOT NULL DEFAULT 0,
    policy_redundancy       TEXT NOT NULL DEFAULT '',
    erasure                 JSONB NOT NULL DEFAULT '{}',
    encryption              JSONB,
    expires_at_unix         BIGINT NOT NULL DEFAULT 0,
    terminated_at_unix      BIGINT NOT NULL DEFAULT 0,
    created_at_height       BIGINT NOT NULL DEFAULT 0,
    finalized_at_height     BIGINT NOT NULL DEFAULT 0,
    indexed_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_intents_user ON intents(user_address);
CREATE INDEX IF NOT EXISTS idx_intents_status ON intents(status);
CREATE INDEX IF NOT EXISTS idx_intents_storage ON intents(storage_status);
CREATE INDEX IF NOT EXISTS idx_intents_file ON intents(file_name);
CREATE INDEX IF NOT EXISTS idx_intents_deal ON intents(deal_id);

-- ============================================================
-- SHARD ASSIGNMENTS
-- ============================================================
CREATE TABLE IF NOT EXISTS shard_assignments (
    id              SERIAL PRIMARY KEY,
    intent_id       TEXT NOT NULL REFERENCES intents(intent_id),
    segment_id      INT NOT NULL,
    shard_index     INT NOT NULL,
    miner_address   TEXT NOT NULL,
    miner_endpoint  TEXT NOT NULL DEFAULT '',
    shard_hash      TEXT NOT NULL DEFAULT '',
    shard_cid       TEXT NOT NULL DEFAULT '',
    shard_size      BIGINT NOT NULL DEFAULT 0,
    committed       BOOLEAN NOT NULL DEFAULT FALSE,
    committed_at    TIMESTAMPTZ,
    UNIQUE(intent_id, segment_id, shard_index)
);
CREATE INDEX IF NOT EXISTS idx_shards_intent ON shard_assignments(intent_id);
CREATE INDEX IF NOT EXISTS idx_shards_miner ON shard_assignments(miner_address);
CREATE INDEX IF NOT EXISTS idx_shards_cid ON shard_assignments(shard_cid);

-- ============================================================
-- MINERS / STORAGE PROVIDERS
-- ============================================================
CREATE TABLE IF NOT EXISTS miners (
    miner_address           TEXT PRIMARY KEY,
    public_key              TEXT NOT NULL DEFAULT '',
    endpoint                TEXT NOT NULL DEFAULT '',
    capacity_bytes          BIGINT NOT NULL DEFAULT 0,
    used_bytes              BIGINT NOT NULL DEFAULT 0,
    reserved_bytes          BIGINT NOT NULL DEFAULT 0,
    stake                   BIGINT NOT NULL DEFAULT 0,
    status                  TEXT NOT NULL DEFAULT 'active',
    proof_success           BIGINT NOT NULL DEFAULT 0,
    proof_failure           BIGINT NOT NULL DEFAULT 0,
    consecutive_failures    BIGINT NOT NULL DEFAULT 0,
    rewards                 BIGINT NOT NULL DEFAULT 0,
    storage_rewards         BIGINT NOT NULL DEFAULT 0,
    retrieval_success       BIGINT NOT NULL DEFAULT 0,
    retrieval_bytes         BIGINT NOT NULL DEFAULT 0,
    retrieval_rewards       BIGINT NOT NULL DEFAULT 0,
    repair_rewards          BIGINT NOT NULL DEFAULT 0,
    slashed                 BIGINT NOT NULL DEFAULT 0,
    effective_weight        BIGINT NOT NULL DEFAULT 0,
    speed_score             BIGINT NOT NULL DEFAULT 0,
    anti_spam_score         BIGINT NOT NULL DEFAULT 0,
    registered_at_unix      BIGINT NOT NULL DEFAULT 0,
    exited_at_unix          BIGINT NOT NULL DEFAULT 0,
    last_seen_unix          BIGINT NOT NULL DEFAULT 0,
    locked_bonus            BIGINT NOT NULL DEFAULT 0,
    bonus_released          BOOLEAN NOT NULL DEFAULT FALSE,
    bonus_expired           BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_miners_status ON miners(status);
CREATE INDEX IF NOT EXISTS idx_miners_capacity ON miners(capacity_bytes DESC);

-- ============================================================
-- VALIDATORS
-- ============================================================
CREATE TABLE IF NOT EXISTS validators (
    owner_address           TEXT PRIMARY KEY,
    operator_address        TEXT NOT NULL DEFAULT '',
    operator_public_key     TEXT NOT NULL DEFAULT '',
    endpoint                TEXT NOT NULL DEFAULT '',
    stake                   BIGINT NOT NULL DEFAULT 0,
    delegated_stake         BIGINT NOT NULL DEFAULT 0,
    self_stake              BIGINT NOT NULL DEFAULT 0,
    status                  TEXT NOT NULL DEFAULT 'active',
    consensus               BOOLEAN NOT NULL DEFAULT FALSE,
    produced_blocks         BIGINT NOT NULL DEFAULT 0,
    slashed                 BIGINT NOT NULL DEFAULT 0,
    evidence_count          BIGINT NOT NULL DEFAULT 0,
    delegator_count         INT NOT NULL DEFAULT 0,
    rewards                 BIGINT NOT NULL DEFAULT 0,
    delegation_rewards      BIGINT NOT NULL DEFAULT 0,
    commission_rate_bps     BIGINT NOT NULL DEFAULT 0,
    registered_at_unix      BIGINT NOT NULL DEFAULT 0,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_validators_status ON validators(status);
CREATE INDEX IF NOT EXISTS idx_validators_consensus ON validators(consensus);
CREATE INDEX IF NOT EXISTS idx_validators_stake ON validators(stake DESC);
CREATE INDEX IF NOT EXISTS idx_validators_operator ON validators(operator_address);

-- ============================================================
-- UNBONDING ENTRIES
-- ============================================================
CREATE TABLE IF NOT EXISTS unbonding_entries (
    id                  TEXT PRIMARY KEY,
    delegator           TEXT NOT NULL,
    validator           TEXT NOT NULL,
    amount              BIGINT NOT NULL DEFAULT 0,
    created_at_unix     BIGINT NOT NULL DEFAULT 0,
    matures_at_unix     BIGINT NOT NULL DEFAULT 0,
    indexed_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_unbonding_delegator ON unbonding_entries(delegator);
CREATE INDEX IF NOT EXISTS idx_unbonding_validator ON unbonding_entries(validator);
CREATE INDEX IF NOT EXISTS idx_unbonding_matures ON unbonding_entries(matures_at_unix);

-- ============================================================
-- STORAGE PROOFS
-- ============================================================
CREATE TABLE IF NOT EXISTS storage_proofs (
    proof_id            SERIAL PRIMARY KEY,
    challenge_id        TEXT NOT NULL,
    epoch_id            TEXT NOT NULL DEFAULT '',
    intent_id           TEXT NOT NULL,
    miner_address       TEXT NOT NULL,
    shard_hash          TEXT NOT NULL DEFAULT '',
    proof_type          TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'pending',
    reward              BIGINT NOT NULL DEFAULT 0,
    slashed             BIGINT NOT NULL DEFAULT 0,
    submitted_at_unix   BIGINT NOT NULL DEFAULT 0,
    indexed_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_proofs_challenge ON storage_proofs(challenge_id);
CREATE INDEX IF NOT EXISTS idx_proofs_intent ON storage_proofs(intent_id);
CREATE INDEX IF NOT EXISTS idx_proofs_miner ON storage_proofs(miner_address);
CREATE INDEX IF NOT EXISTS idx_proofs_epoch ON storage_proofs(epoch_id);

-- ============================================================
-- PROOF EPOCHS
-- ============================================================
CREATE TABLE IF NOT EXISTS proof_epochs (
    epoch_id                TEXT PRIMARY KEY,
    epoch_round             BIGINT NOT NULL DEFAULT 0,
    intent_id               TEXT NOT NULL DEFAULT '',
    challenge_count         INT NOT NULL DEFAULT 0,
    started_at_unix         BIGINT NOT NULL DEFAULT 0,
    deadline_unix           BIGINT NOT NULL DEFAULT 0,
    status                  TEXT NOT NULL DEFAULT 'active',
    accepted_proofs         INT NOT NULL DEFAULT 0,
    missed_proofs           INT NOT NULL DEFAULT 0,
    storage_rewards_paid    BIGINT NOT NULL DEFAULT 0,
    retrieval_rewards_paid  BIGINT NOT NULL DEFAULT 0,
    repair_rewards_paid     BIGINT NOT NULL DEFAULT 0,
    storage_slashed         BIGINT NOT NULL DEFAULT 0,
    repair_tasks_created    INT NOT NULL DEFAULT 0,
    indexed_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_epochs_status ON proof_epochs(status);
CREATE INDEX IF NOT EXISTS idx_epochs_intent ON proof_epochs(intent_id);

-- ============================================================
-- DAILY STATS (pre-aggregated)
-- ============================================================
CREATE TABLE IF NOT EXISTS daily_stats (
    date                    DATE PRIMARY KEY,
    tx_count                BIGINT NOT NULL DEFAULT 0,
    active_addresses        INT NOT NULL DEFAULT 0,
    new_intents             INT NOT NULL DEFAULT 0,
    finalized_intents       INT NOT NULL DEFAULT 0,
    data_uploaded_bytes     BIGINT NOT NULL DEFAULT 0,
    data_retrieved_bytes    BIGINT NOT NULL DEFAULT 0,
    storage_rewards         BIGINT NOT NULL DEFAULT 0,
    retrieval_rewards       BIGINT NOT NULL DEFAULT 0,
    repair_rewards          BIGINT NOT NULL DEFAULT 0,
    total_slashed           BIGINT NOT NULL DEFAULT 0,
    avg_fee                 BIGINT NOT NULL DEFAULT 0,
    blocks_produced         INT NOT NULL DEFAULT 0,
    new_accounts            INT NOT NULL DEFAULT 0,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- SYNC STATE (tracker)
-- ============================================================
CREATE TABLE IF NOT EXISTS sync_state (
    key             TEXT PRIMARY KEY,
    value           TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO sync_state (key, value) VALUES ('latest_height', '0') ON CONFLICT DO NOTHING;

COMMIT;
