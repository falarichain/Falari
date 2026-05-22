package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := "postgres://localhost:5432/falari_explorer?sslmode=disable"
	if len(os.Args) > 1 {
		dbURL = os.Args[1]
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	log.Println("Connected to database. Seeding mock data...")

	// Clear existing data
	pool.Exec(ctx, `TRUNCATE TABLE daily_stats, storage_proofs, proof_epochs, shard_assignments, intents, transactions, blocks, accounts, miners, validators CASCADE`)
	pool.Exec(ctx, `INSERT INTO sync_state (key, value) VALUES ('latest_height', '0') ON CONFLICT (key) DO UPDATE SET value = '0'`)

	now := time.Now().Unix()
	daySec := int64(86400)

	// ====== Addresses ======
	accounts := []struct{ addr, pk string }{
		{"falari1q8hckq0v4l7dj9y8ldx2wzfjw9kxpq3lqp9x50", "ed25519pub1a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0"},
		{"falari1p9ms8vrl7t8xq5n4z9qedwxy6h7a5s3d2f1g0a", "ed25519pub1b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9b1"},
		{"falari1qve9f3l8d8k2j7h6g5f4e3d2c1b0a9z8y7x6w", "ed25519pub1c1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9c2"},
		{"falari1m4n5b6v7c8x9z0a1s2d3f4g5h6j7k8l9p0q1w", "ed25519pub1d1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9d3"},
		{"falari1k2j3h4g5f6d7s8a9p0o9i8u7y6t5r4e3w2q1", "ed25519pub1e1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9e4"},
		{"falari1z9x8c7v6b5n4m3a2s1d2f3g4h5j6k7l8p9o0", "ed25519pub1f1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9f5"},
		// Miner addresses
		{"falari1miner01abcdefghijklmnopqrstuvwxyz", "ed25519pub1m01abcdefghijklmnopqrstuvwxyz0123456789ab"},
		{"falari1miner02bcdefghijklmnopqrstuvwxyza", "ed25519pub1m02bcdefghijklmnopqrstuvwxyza0123456789bc"},
		{"falari1miner03cdefghijklmnopqrstuvwxyzab", "ed25519pub1m03cdefghijklmnopqrstuvwxyzab0123456789cd"},
		{"falari1miner04defghijklmnopqrstuvwxyzabc", "ed25519pub1m04defghijklmnopqrstuvwxyzabc0123456789de"},
		{"falari1miner05efghijklmnopqrstuvwxyzabcd", "ed25519pub1m05efghijklmnopqrstuvwxyzabcd0123456789ef"},
		// Validator addresses
		{"falari1val01abcdefghijklmnopqrstuvwxyz", "ed25519pub1v01abcdefghijklmnopqrstuvwxyz0123456789fg"},
		{"falari1val02bcdefghijklmnopqrstuvwxyza", "ed25519pub1v02bcdefghijklmnopqrstuvwxyza0123456789gh"},
		{"falari1val03cdefghijklmnopqrstuvwxyzab", "ed25519pub1v03cdefghijklmnopqrstuvwxyzab0123456789hi"},
		{"falari1val04defghijklmnopqrstuvwxyzabc", "ed25519pub1v04defghijklmnopqrstuvwxyzabc0123456789ij"},
		{"falari1val05efghijklmnopqrstuvwxyzabcd", "ed25519pub1v05efghijklmnopqrstuvwxyzabcd0123456789jk"},
	}

	// ====== Accounts ======
	log.Println("Inserting accounts...")
	for _, a := range accounts {
		_, err := pool.Exec(ctx, `
			INSERT INTO accounts (address, public_key, balance, nonce, locked_stake, locked_storage, first_seen_height, last_updated_height)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (address) DO NOTHING
		`, a.addr, a.pk, randomInt(1000000, 50000000), randomInt(0, 100), randomInt(0, 1000000), randomInt(0, 500000), 1, 20)
		if err != nil {
			log.Fatalf("Insert account: %v", err)
		}
	}
	log.Printf("  -> %d accounts", len(accounts))

	// ====== Miners ======
	log.Println("Inserting miners...")
	miners := []string{"falari1miner01abcdefghijklmnopqrstuvwxyz", "falari1miner02bcdefghijklmnopqrstuvwxyza", "falari1miner03cdefghijklmnopqrstuvwxyzab", "falari1miner04defghijklmnopqrstuvwxyzabc", "falari1miner05efghijklmnopqrstuvwxyzabcd"}
	minerEndpoints := []string{"https://node1.falari.io:9090", "https://node2.falari.io:9090", "https://node3.falari.io:9090", "https://node4.falari.io:9090", "https://node5.falari.io:9090"}
	minerPKs := []string{"ed25519pub1m01", "ed25519pub1m02", "ed25519pub1m03", "ed25519pub1m04", "ed25519pub1m05"}
	minerStatuses := []string{"active", "active", "active", "maintenance", "active"}

	for i, addr := range miners {
		pk := minerPKs[i]
		ep := minerEndpoints[i]
		st := minerStatuses[i]
		_, err := pool.Exec(ctx, `
			INSERT INTO miners (miner_address, public_key, endpoint, capacity_bytes, used_bytes, reserved_bytes, stake, status,
				proof_success, proof_failure, consecutive_failures, rewards, storage_rewards, retrieval_success, retrieval_bytes, retrieval_rewards, repair_rewards, slashed,
				effective_weight, speed_score, anti_spam_score, registered_at_unix, last_seen_unix)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
			ON CONFLICT (miner_address) DO NOTHING
		`, addr, pk, ep,
			randomInt64(500000000000, 2000000000000), // capacity 500GB-2TB
			randomInt64(100000000000, 1500000000000), // used
			randomInt64(10000000000, 100000000000),  // reserved
			randomInt64(100000000, 10000000000),     // stake
			st,
			randomInt64(100, 5000), // proof_success
			randomInt64(0, 50),     // proof_failure
			int64(i*2),             // consecutive_failures
			randomInt64(1000000, 50000000), // rewards
			randomInt64(1000000, 30000000), // storage_rewards
			randomInt64(50, 1000),          // retrieval_success
			randomInt64(500000000, 50000000000), // retrieval_bytes
			randomInt64(500000, 10000000),       // retrieval_rewards
			randomInt64(100000, 5000000),        // repair_rewards
			randomInt64(0, 2000000),             // slashed
			randomInt64(50, 100),    // effective_weight
			randomInt64(70, 100),    // speed_score
			randomInt64(80, 100),    // anti_spam_score
			now-int64(30+daysAgo(i*5)), // registered_at
			now-int64(i*2*3600),        // last_seen
		)
		if err != nil {
			log.Fatalf("Insert miner: %v", err)
		}
	}
	log.Printf("  -> %d miners", len(miners))

	// ====== Validators ======
	log.Println("Inserting validators...")
	validators := []string{"falari1val01abcdefghijklmnopqrstuvwxyz", "falari1val02bcdefghijklmnopqrstuvwxyza", "falari1val03cdefghijklmnopqrstuvwxyzab", "falari1val04defghijklmnopqrstuvwxyzabc", "falari1val05efghijklmnopqrstuvwxyzabcd"}
	valPKs := []string{"ed25519pub1v01", "ed25519pub1v02", "ed25519pub1v03", "ed25519pub1v04", "ed25519pub1v05"}
	for i, addr := range validators {
		pk := valPKs[i]
		consensus := i < 3 // first 3 in consensus
		_, err := pool.Exec(ctx, `
			INSERT INTO validators (validator_address, public_key, endpoint, stake, delegated_stake, self_stake, status, consensus,
				produced_blocks, slashed, evidence_count, delegator_count, rewards, delegation_rewards, registered_at_unix)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (validator_address) DO NOTHING
		`, addr, pk, fmt.Sprintf("https://val%d.falari.io:26657", i+1),
			randomInt64(500000000, 10000000000),    // stake
			randomInt64(100000000, 5000000000),     // delegated_stake
			randomInt64(400000000, 5000000000),     // self_stake
			"active",
			consensus,
			randomInt64(50, 5000),            // produced_blocks
			randomInt64(0, 1000000),           // slashed
			randomInt64(0, 5),                // evidence_count
			randomInt(5, 50),                 // delegator_count
			randomInt64(100000, 50000000),    // rewards
			randomInt64(50000, 20000000),     // delegation_rewards
			now-int64(60+daysAgo(i*3)),
		)
		if err != nil {
			log.Fatalf("Insert validator: %v", err)
		}
	}
	log.Printf("  -> %d validators", len(validators))

	// ====== Intents ======
	log.Println("Inserting intents...")
	intentIDs := make([]string, 0)
	intentsData := []struct {
		id, user, fileName, status, storageStatus, policyClass string
		fileSize, dataShards, parityShards, shardSize          int64
	}{
		{"INT-FILE-001", accounts[0].addr, "ai-training-dataset.tar.gz", "active", "stored", "archival", 50_000_000_000, 10, 4, 1024 * 1024},
		{"INT-FILE-002", accounts[1].addr, "blockchain-whitepaper.pdf", "active", "stored", "standard", 2_500_000, 6, 3, 256 * 1024},
		{"INT-FILE-003", accounts[2].addr, "user-profiles-backup.sql", "finalizing", "storing", "standard", 1_200_000_000, 8, 4, 512 * 1024},
		{"INT-FILE-004", accounts[0].addr, "video-archive-2025.mp4", "active", "stored", "media", 200_000_000_000, 12, 5, 4 * 1024 * 1024},
		{"INT-FILE-005", accounts[3].addr, "corporate-documents.zip", "expired", "expired", "archival", 15_000_000_000, 10, 4, 1 * 1024 * 1024},
		{"INT-FILE-006", accounts[4].addr, "nft-metadata-collection.json", "active", "stored", "standard", 500_000, 4, 2, 128 * 1024},
		{"INT-FILE-007", accounts[5].addr, "scientific-simulation-data.h5", "uploading", "pending", "archival", 500_000_000_000, 16, 6, 8 * 1024 * 1024},
		{"INT-FILE-008", accounts[1].addr, "mobile-app-backup.7z", "finalizing", "storing", "standard", 8_000_000_000, 8, 4, 512 * 1024},
	}

	for _, d := range intentsData {
		intentIDs = append(intentIDs, d.id)
		expiresAt := now + int64(daysAgo(-365)) // 1 year from now
		if d.status == "expired" {
			expiresAt = now - daySec*30
		}
		erasure := map[string]interface{}{
			"data_shards":   d.dataShards,
			"parity_shards": d.parityShards,
			"shard_size":    d.shardSize,
			"algorithm":     "reed-solomon",
		}
		erasureJSON, _ := json.Marshal(erasure)
		_, err := pool.Exec(ctx, `
			INSERT INTO intents (intent_id, user_address, file_name, file_size, segment_size, file_root, deal_id, manifest_root,
				status, storage_status, access_status, moderation_status, locked_fee, paid_fee, refunded_fee,
				uploaded_size, committed_segments, data_shards, parity_shards, shard_size, policy_class, policy_duration,
				policy_redundancy, erasure, expires_at_unix, terminated_at_unix, created_at_height, finalized_at_height)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28)
			ON CONFLICT (intent_id) DO NOTHING
		`, d.id, d.user, d.fileName, d.fileSize, d.shardSize*d.dataShards, randomHash(), "DEAL-"+randomHex(8), randomHash(),
			d.status, d.storageStatus, "public", "none",
			randomInt64(100000, 10000000), randomInt64(50000, 5000000), randomInt64(0, 100000),
			randomInt64(0, d.fileSize), randomInt(0, int(d.dataShards)), d.dataShards, d.parityShards, d.shardSize,
			d.policyClass, int64(365*24*3600), "3x", string(erasureJSON), expiresAt, 0,
			randomInt64(1, 15), randomInt64(0, 20))
		if err != nil {
			log.Fatalf("Insert intent %s: %v", d.id, err)
		}
	}
	log.Printf("  -> %d intents", len(intentsData))

	// ====== Blocks ======
	log.Println("Inserting blocks...")
	producers := []string{"falari1val01abcdefghijklmnopqrstuvwxyz", "falari1val02bcdefghijklmnopqrstuvwxyza", "falari1val03cdefghijklmnopqrstuvwxyzab"}
	blockIDs := make([]int64, 0)
	for i := int64(1); i <= 20; i++ {
		blockTime := now - (21-i)*int64(10) // 10s apart
		producer := producers[int(i)%len(producers)]
		txCount := int64(3 + randIntN(6)) // 3-8 txs per block
		_, err := pool.Exec(ctx, `
			INSERT INTO blocks (height, hash, prev_hash, round, time_unix, tx_root, state_root, receipts_root,
				producer_address, producer_public_key, signature, finalized, voting_power, total_power, tx_count)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (height) DO NOTHING
		`, i, randomHash(), randomHash(), i, blockTime, randomHash(), randomHash(), randomHash(),
			producer, "ed25519pub1v0"+fmt.Sprint((i%5)+1), randomHex(128), true,
			randomInt64(50000, 100000), 100000, txCount)
		if err != nil {
			log.Fatalf("Insert block %d: %v", i, err)
		}
		blockIDs = append(blockIDs, i)
	}
	log.Printf("  -> %d blocks", len(blockIDs))

	// ====== Transactions ======
	log.Println("Inserting transactions...")
	txTypes := []string{"transfer", "create_intent", "batch_commit", "register_miner", "register_validator", "finalize", "stake", "unstake"}
	type txPlan struct {
		txType string
		from   string
		to     string
		payload map[string]interface{}
	}
	var txPlans []txPlan
	txIdx := 0

	// Generate diverse transaction plans
	for bi := int64(0); bi < 20; bi++ {
		count := int64(3 + randIntN(6))
		for ti := int64(0); ti < count; ti++ {
			txType := txTypes[randIntN(len(txTypes))]
			fromAcct := accounts[randIntN(6)]
			plan := txPlan{txType: txType, from: fromAcct.addr}
			switch txType {
			case "transfer":
				toAcct := accounts[randIntN(6)]
				plan.to = toAcct.addr
				plan.payload = map[string]interface{}{"to": toAcct.addr, "amount": randomInt64(1000, 50000000)}
			case "create_intent":
				intentFile := []string{"project-backup.tar.gz", "dataset-v3.h5", "documents.zip", "config-files.jsonl", "logs-archive.7z"}[randIntN(5)]
				fileSize := int64([]int{1000000000, 50000000000, 100000000, 5000000, 20000000000}[randIntN(5)])
				plan.payload = map[string]interface{}{
					"user":          fromAcct.addr,
					"file_name":     intentFile,
					"file_size":     fileSize,
					"data_shards":   randomInt(3, 20),
					"parity_shards": randomInt(2, 6),
					"shard_size":    randomInt64(65536, 16777216),
				}
			case "register_miner":
				minerAddr := miners[randIntN(len(miners))]
				plan.payload = map[string]interface{}{
					"miner_address": minerAddr,
					"endpoint":      minerEndpoints[randIntN(len(minerEndpoints))],
					"capacity":      randomInt64(100000000000, 2000000000000),
					"stake":         randomInt64(10000000, 1000000000),
				}
			case "register_validator":
				valAddr := validators[randIntN(len(validators))]
				plan.payload = map[string]interface{}{
					"address":  valAddr,
					"endpoint": fmt.Sprintf("https://val%d.falari.io:26657", randIntN(5)+1),
					"stake":    randomInt64(100000000, 5000000000),
				}
			case "batch_commit":
				intentIdx := randIntN(len(intentIDs))
				plan.payload = map[string]interface{}{
					"intent_id":   intentIDs[intentIdx],
					"segment_id":  randomInt(0, 5),
					"commitments": randomInt(1, 10),
				}
			case "finalize":
				intentIdx := randIntN(len(intentIDs))
				plan.payload = map[string]interface{}{"intent_id": intentIDs[intentIdx]}
			case "stake":
				plan.payload = map[string]interface{}{"amount": randomInt64(1000000, 100000000), "validator": validators[randIntN(3)]}
			case "unstake":
				plan.payload = map[string]interface{}{"amount": randomInt64(500000, 50000000), "validator": validators[randIntN(5)]}
			}
			txPlans = append(txPlans, plan)
			txIdx++
		}
	}

	offset := 0
	for bi := int64(0); bi < 20; bi++ {
		bh := bi + 1
		count := int64(3 + randIntN(6))
		for ti := int64(0); ti < count && offset < len(txPlans); ti++ {
			plan := txPlans[offset]
			offset++
			txID := "TX-" + randomHex(32)
			payloadJSON, _ := json.Marshal(plan.payload)
			nonce := ti + 1
			_, err := pool.Exec(ctx, `
				INSERT INTO transactions (tx_id, block_height, tx_type, from_address, nonce, fee, payload_hash, payload, created_at_unix)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (tx_id) DO NOTHING
			`, txID, bh, plan.txType, plan.from, nonce,
				randomInt64(100, 1000000), randomHash(),
				string(payloadJSON),
				now-(21-bh)*10)
			if err != nil {
				log.Fatalf("Insert tx %s: %v", txID, err)
			}
		}
	}
	log.Printf("  -> %d transactions", len(txPlans))

	// ====== Shard Assignments ======
	log.Println("Inserting shard assignments...")
	shardCount := 0
	for _, intentID := range intentIDs {
		numSegments := 3 + randIntN(5)
		for seg := 0; seg < numSegments; seg++ {
			numShards := 3 + randIntN(7)
			for sh := 0; sh < numShards; sh++ {
				miner := miners[randIntN(len(miners))]
				_, err := pool.Exec(ctx, `
					INSERT INTO shard_assignments (intent_id, segment_id, shard_index, miner_address, miner_endpoint, shard_hash, shard_cid, shard_size, committed, committed_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
					ON CONFLICT (intent_id, segment_id, shard_index) DO NOTHING
				`, intentID, seg, sh, miner, "https://miner.falari.io:9090", randomHash(),
					"bafybei"+randomHex(40), randomInt64(65536, 16777216),
					randIntN(2) == 1, time.Now().Add(-time.Duration(randIntN(72))*time.Hour))
				if err != nil {
					log.Fatalf("Insert shard: %v", err)
				}
				shardCount++
			}
		}
	}
	log.Printf("  -> %d shard assignments", shardCount)

	// ====== Proof Epochs ======
	log.Println("Inserting proof epochs...")
	for i := 0; i < 8; i++ {
		epochID := "EPOCH-" + randomHex(12)
		intentID := intentIDs[randIntN(len(intentIDs))]
		status := []string{"active", "active", "completed", "completed", "completed", "missed"}[randIntN(6)]
		_, err := pool.Exec(ctx, `
			INSERT INTO proof_epochs (epoch_id, epoch_round, intent_id, challenge_count, started_at_unix, deadline_unix, status,
				accepted_proofs, missed_proofs, storage_rewards_paid, retrieval_rewards_paid, repair_rewards_paid, storage_slashed, repair_tasks_created)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (epoch_id) DO NOTHING
		`, epochID, int64(i+1), intentID, randomInt(5, 50),
			now-int64((8-i)*3600), now-int64((8-i)*3600)+3600, status,
			randomInt(3, 45), randomInt(0, 10),
			randomInt64(10000, 500000), randomInt64(5000, 200000), randomInt64(1000, 100000),
			randomInt64(0, 100000), randomInt(0, 5))
		if err != nil {
			log.Fatalf("Insert epoch: %v", err)
		}
	}
	log.Println("  -> 8 proof epochs")

	// ====== Storage Proofs ======
	log.Println("Inserting storage proofs...")
	for i := 0; i < 30; i++ {
		challengeID := "CHALL-" + randomHex(16)
		intentID := intentIDs[randIntN(len(intentIDs))]
		miner := miners[randIntN(len(miners))]
		status := []string{"accepted", "accepted", "accepted", "accepted", "rejected", "missed", "pending"}[randIntN(7)]
		_, err := pool.Exec(ctx, `
			INSERT INTO storage_proofs (challenge_id, epoch_id, intent_id, miner_address, shard_hash, proof_type, status, reward, slashed, submitted_at_unix)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, challengeID, "EPOCH-"+randomHex(12), intentID, miner, randomHash(),
			[]string{"spacetime", "merkle", "zk-snark", "zak"}[randIntN(4)],
			status,
			randomInt64(1000, 100000), randomInt64(0, 50000),
			now-randomInt64(0, 604800))
		if err != nil {
			log.Fatalf("Insert proof: %v", err)
		}
	}
	log.Println("  -> 30 storage proofs")

	// ====== Daily Stats ======
	log.Println("Inserting daily stats...")
	for d := 6; d >= 0; d-- {
		date := time.Now().AddDate(0, 0, -d).Format("2006-01-02")
		_, err := pool.Exec(ctx, `
			INSERT INTO daily_stats (date, tx_count, active_addresses, new_intents, finalized_intents, data_uploaded_bytes,
				data_retrieved_bytes, storage_rewards, retrieval_rewards, repair_rewards, total_slashed, avg_fee, blocks_produced, new_accounts)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (date) DO NOTHING
		`, date, randomInt64(500, 3000), randomInt(50, 200), randomInt(2, 15), randomInt(1, 12),
			randomInt64(500000000, 50000000000), randomInt64(100000000, 10000000000),
			randomInt64(100000, 10000000), randomInt64(50000, 5000000),
			randomInt64(10000, 1000000), randomInt64(0, 500000), randomInt64(50, 500),
			randomInt(10, 50), randomInt(1, 20))
		if err != nil {
			log.Fatalf("Insert daily stats: %v", err)
		}
	}
	log.Println("  -> 7 daily stats")

	// Update sync state
	pool.Exec(ctx, `INSERT INTO sync_state (key, value) VALUES ('latest_height', '20') ON CONFLICT (key) DO UPDATE SET value = '20'`)

	log.Println("\n==============================")
	log.Println("Mock data seeding complete!")
	log.Println("==============================")
	log.Printf("  Accounts:    %d", len(accounts))
	log.Printf("  Miners:      %d", len(miners))
	log.Printf("  Validators:  %d", len(validators))
	log.Printf("  Intents:     %d", len(intentsData))
	log.Printf("  Blocks:      20")
	log.Printf("  Txs:         %d", len(txPlans))
	log.Printf("  Shards:      %d", shardCount)
	log.Printf("  Proofs:      30")
	log.Printf("  Epochs:      8")
	log.Printf("  Daily Stats: 7")
}

func randomHash() string {
	return strings.ToUpper(randomHex(64))
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	h := hex.EncodeToString(b)
	return h[:n]
}

func randomInt(min, max int) int {
	return min + randIntN(max-min+1)
}

func randomInt64(min, max int64) int64 {
	return min + int64(randIntN(int(max-min+1)))
}

func randIntN(n int) int {
	b := make([]byte, 4)
	rand.Read(b)
	val := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	if val < 0 {
		val = -val
	}
	return val % n
}

func daysAgo(n int) int64 {
	return int64(n) * 86400
}
