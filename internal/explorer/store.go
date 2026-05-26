package explorer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"chain/internal/client"
	"chain/internal/wire"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the PostgreSQL connection for the explorer.
type Store struct {
	pool     *pgxpool.Pool
	chainURL string
}

// NewStore creates a new explorer store connected to PostgreSQL.
func NewStore(ctx context.Context, databaseURL, chainURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 10

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	log.Printf("explorer: connected to PostgreSQL")
	return &Store{pool: pool, chainURL: chainURL}, nil
}

// Close closes the database pool.
func (s *Store) Close() {
	s.pool.Close()
}

// ============================================================
// Sync
// ============================================================

// Sync polls the chain for new blocks and indexes them into PostgreSQL.
func (s *Store) Sync(ctx context.Context) error {
	status, err := s.fetchStatus()
	if err != nil {
		return fmt.Errorf("fetch status: %w", err)
	}

	// read current synced height from db
	var latestHeight uint64
	err = s.pool.QueryRow(ctx,
		"SELECT COALESCE(MAX(height), 0) FROM blocks",
	).Scan(&latestHeight)
	if err != nil {
		return fmt.Errorf("read latest height: %w", err)
	}

	for h := latestHeight + 1; h <= status.Height && h <= latestHeight+100; h++ {
		block, err := s.fetchBlock(h)
		if err != nil {
			log.Printf("explorer: fetch block %d error: %v", h, err)
			break
		}
		if err := s.indexBlock(ctx, block); err != nil {
			log.Printf("explorer: index block %d error: %v", h, err)
			break
		}
	}

	// also refresh intents/miners/validators/accounts periodically
	s.refreshIntents(ctx)
	s.refreshMiners(ctx)
	s.refreshValidators(ctx)
	s.refreshAccounts(ctx)
	s.computeDailyStats(ctx)

	return nil
}

func (s *Store) indexBlock(ctx context.Context, block wire.Block) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// insert block
	_, err = tx.Exec(ctx, `
		INSERT INTO blocks (height, hash, prev_hash, round, time_unix, tx_root, state_root,
			receipts_root, producer_address, producer_public_key, signature, finalized,
			voting_power, total_power, tx_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (height) DO NOTHING
	`, block.Height, block.Hash, block.PrevHash, block.Round, block.TimeUnix,
		block.TxRoot, block.StateRoot, block.ReceiptsRoot,
		block.ProducerAddress, block.ProducerPublicKey, block.Signature,
		block.Finality.Finalized, block.Finality.VotingPower, block.Finality.TotalPower,
		len(block.Transactions))
	if err != nil {
		return fmt.Errorf("insert block: %w", err)
	}

	// insert transactions
	for _, txData := range block.Transactions {
		payloadJSON, _ := json.Marshal(txData.Payload)
		_, err = tx.Exec(ctx, `
			INSERT INTO transactions (tx_id, block_height, tx_type, from_address,
				nonce, nonce_protected, agent_key_id, agent_nonce, fee, payload_hash,
				payload, created_at_unix)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT (tx_id) DO NOTHING
		`, txData.TxID, block.Height, txData.Type, txData.From,
			txData.Nonce, txData.NonceProtected, txData.AgentKeyID, txData.AgentNonce,
			txData.Fee, txData.PayloadHash, payloadJSON, txData.CreatedAtUnix)
		if err != nil {
			return fmt.Errorf("insert tx %s: %w", txData.TxID, err)
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) refreshIntents(ctx context.Context) {
	var resp struct {
		Intents []wire.IntentView `json:"intents"`
	}
	if err := client.NewHTTP(s.chainURL).Get("/intents", &resp); err != nil {
		log.Printf("explorer: fetch intents error: %v", err)
		return
	}

	for _, intent := range resp.Intents {
		erasureJSON, _ := json.Marshal(intent.Erasure)
		var encryptionJSON []byte
		if intent.Encryption != nil {
			encryptionJSON, _ = json.Marshal(intent.Encryption)
		}

		_, err := s.pool.Exec(ctx, `
			INSERT INTO intents (intent_id, user_address, file_name, file_size, segment_size,
				file_root, deal_id, manifest_root, status, storage_status, access_status,
				moderation_status, locked_fee, paid_fee, refunded_fee, uploaded_size,
				committed_segments, data_shards, parity_shards, shard_size,
				policy_class, policy_duration, policy_redundancy,
				erasure, encryption, expires_at_unix, terminated_at_unix)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
			ON CONFLICT (intent_id) DO UPDATE SET
				status = EXCLUDED.status,
				storage_status = EXCLUDED.storage_status,
				access_status = EXCLUDED.access_status,
				moderation_status = EXCLUDED.moderation_status,
				committed_segments = EXCLUDED.committed_segments,
				uploaded_size = EXCLUDED.uploaded_size,
				paid_fee = EXCLUDED.paid_fee,
				refunded_fee = EXCLUDED.refunded_fee,
				deal_id = EXCLUDED.deal_id,
				expires_at_unix = EXCLUDED.expires_at_unix,
				terminated_at_unix = EXCLUDED.terminated_at_unix
		`, intent.IntentID, intent.User, intent.FileName, intent.FileSize, intent.SegmentSize,
			intent.FileRoot, intent.DealID, "", intent.Status, intent.StorageStatus,
			intent.AccessStatus, intent.ModerationStatus,
			intent.LockedFee, intent.PaidFee, intent.RefundedFee, intent.UploadedSize,
			intent.CommittedSegments, intent.Erasure.DataShards, intent.Erasure.ParityShards,
			intent.Erasure.ShardSize,
			intent.Policy.Class, intent.Policy.Duration, intent.Policy.Redundancy,
			erasureJSON, encryptionJSON, intent.ExpiresAtUnix, intent.TerminatedAtUnix)
		if err != nil {
			log.Printf("explorer: insert intent %s error: %v", intent.IntentID, err)
		}

		// shard assignments
		for _, a := range intent.Assignments {
			_, err = s.pool.Exec(ctx, `
				INSERT INTO shard_assignments (intent_id, segment_id, shard_index, miner_address,
					miner_endpoint, shard_hash, shard_cid, shard_size, committed)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true)
				ON CONFLICT (intent_id, segment_id, shard_index) DO UPDATE SET
					miner_address = EXCLUDED.miner_address,
					miner_endpoint = EXCLUDED.miner_endpoint,
					shard_cid = EXCLUDED.shard_cid
			`, intent.IntentID, a.SegmentID, a.ShardIndex, a.MinerAddress,
				a.Endpoint, a.ShardHash, a.ShardCID, a.ShardSize)
			if err != nil {
				log.Printf("explorer: insert shard error: %v", err)
			}
		}
	}
}

func (s *Store) refreshMiners(ctx context.Context) {
	var resp struct {
		Providers []wire.StorageProviderRecord `json:"providers"`
	}
	if err := client.NewHTTP(s.chainURL).Get("/storage/providers", &resp); err != nil {
		log.Printf("explorer: fetch providers error: %v", err)
		return
	}
	for _, p := range resp.Providers {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO miners (miner_address, public_key, endpoint, capacity_bytes, used_bytes,
				status, last_seen_unix)
			VALUES ($1,$2,$3,$4,$5,'active',$6)
			ON CONFLICT (miner_address) DO UPDATE SET
				endpoint = EXCLUDED.endpoint,
				capacity_bytes = EXCLUDED.capacity_bytes,
				used_bytes = EXCLUDED.used_bytes,
				last_seen_unix = EXCLUDED.last_seen_unix
		`, p.MinerAddress, p.PublicKey, p.Endpoint, p.CapacityBytes, p.StoredBytes, time.Now().Unix())
		if err != nil {
			log.Printf("explorer: insert miner %s error: %v", p.MinerAddress, err)
		}
	}
}

func (s *Store) refreshValidators(ctx context.Context) {
	var resp struct {
		Validators []wire.ValidatorInfo `json:"validators"`
	}
	if err := client.NewHTTP(s.chainURL).Get("/validators", &resp); err != nil {
		log.Printf("explorer: fetch validators error: %v", err)
		return
	}
	for _, v := range resp.Validators {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO validators (owner_address, operator_address, operator_public_key, endpoint, stake,
				delegated_stake, self_stake, status, consensus, produced_blocks, slashed,
				evidence_count, delegator_count, rewards, delegation_rewards, registered_at_unix, commission_rate_bps)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT (owner_address) DO UPDATE SET
				operator_address = EXCLUDED.operator_address,
				operator_public_key = EXCLUDED.operator_public_key,
				stake = EXCLUDED.stake,
				delegated_stake = EXCLUDED.delegated_stake,
				self_stake = EXCLUDED.self_stake,
				status = EXCLUDED.status,
				consensus = EXCLUDED.consensus,
				produced_blocks = EXCLUDED.produced_blocks,
				slashed = EXCLUDED.slashed,
				evidence_count = EXCLUDED.evidence_count,
				rewards = EXCLUDED.rewards,
				commission_rate_bps = EXCLUDED.commission_rate_bps
		`, v.OwnerAddress, v.OperatorAddress, v.OperatorPublicKey, v.Endpoint, v.Stake, v.DelegatedStake,
			v.SelfStake, v.Status, v.Consensus, v.ProducedBlocks, v.Slashed,
			v.EvidenceCount, v.DelegatorCount, v.Rewards, v.DelegationRewards,
			v.RegisteredAtUnix, v.CommissionRateBPS)
		if err != nil {
			log.Printf("explorer: insert validator %s error: %v", v.OwnerAddress, err)
		}
	}
}

func (s *Store) refreshAccounts(ctx context.Context) {
	status, err := s.fetchStatus()
	if err != nil {
		return
	}
	// We just update the total; individual accounts are indexed lazily on query
	_ = status
}

func (s *Store) computeDailyStats(ctx context.Context) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO daily_stats (date, tx_count, blocks_produced)
		SELECT $1,
			COUNT(*) FILTER (WHERE created_at_unix >= $2),
			COUNT(DISTINCT b.height)
		FROM transactions t
		JOIN blocks b ON b.height = t.block_height
		WHERE t.created_at_unix >= $2
		ON CONFLICT (date) DO UPDATE SET
			tx_count = EXCLUDED.tx_count,
			blocks_produced = EXCLUDED.blocks_produced
	`, today, today.Unix())
	if err != nil {
		log.Printf("explorer: compute daily stats error: %v", err)
	}
}

// ============================================================
// Chain API helpers
// ============================================================

func (s *Store) fetchStatus() (wire.ChainStatusResponse, error) {
	var status wire.ChainStatusResponse
	if err := client.NewHTTP(s.chainURL).Get("/status", &status); err != nil {
		return wire.ChainStatusResponse{}, err
	}
	return status, nil
}

func (s *Store) fetchBlock(height uint64) (wire.Block, error) {
	var block wire.Block
	if err := client.NewHTTP(s.chainURL).Get("/blocks/"+u64toa(height), &block); err != nil {
		return wire.Block{}, err
	}
	return block, nil
}

// ============================================================
// Query helpers
// ============================================================

// Pool returns the connection pool (for API handlers).
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// QueryRow is a convenience wrapper.
func (s *Store) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return s.pool.QueryRow(ctx, sql, args...)
}

// Query is a convenience wrapper.
func (s *Store) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return s.pool.Query(ctx, sql, args...)
}

// ============================================================
// Sync info
// ============================================================

// SyncInfo returns the latest sync state.
func (s *Store) SyncInfo(ctx context.Context) map[string]any {
	var height uint64
	var blockCount, txCount, intentCount, minerCount, validatorCount int64

	s.pool.QueryRow(ctx, "SELECT COALESCE(MAX(height), 0) FROM blocks").Scan(&height)
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM blocks").Scan(&blockCount)
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions").Scan(&txCount)
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM intents").Scan(&intentCount)
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM miners").Scan(&minerCount)
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM validators").Scan(&validatorCount)

	return map[string]any{
		"latest_height":     height,
		"blocks_indexed":    blockCount,
		"transactions":      txCount,
		"intents_indexed":   intentCount,
		"miners_indexed":    minerCount,
		"validators_indexed": validatorCount,
	}
}

func u64toa(v uint64) string {
	if v == 0 {
		return "0"
	}
	return fmt.Sprintf("%d", v)
}
