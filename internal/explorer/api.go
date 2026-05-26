package explorer

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"chain/internal/client"
	"chain/internal/wire"
)

// Server serves the Explorer REST API.
type Server struct {
	store *Store
}

// NewServer creates a new explorer API server.
func NewServer(store *Store) *Server {
	return &Server{store: store}
}

// Routes returns the HTTP handler with all explorer API routes.
// CORS and rate limiting should be applied by the caller.
func (srv *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// ---- Dashboard / status ----
	mux.HandleFunc("GET /api/status", srv.handleStatus)

	// ---- Blocks ----
	mux.HandleFunc("GET /api/blocks", srv.handleBlocks)
	mux.HandleFunc("GET /api/blocks/latest", srv.handleLatestBlock)
	mux.HandleFunc("GET /api/blocks/{heightOrHash}", srv.handleBlock)

	// ---- Transactions ----
	mux.HandleFunc("GET /api/txs", srv.handleTransactions)
	mux.HandleFunc("GET /api/txs/{txID}", srv.handleTransaction)

	// ---- Accounts ----
	mux.HandleFunc("GET /api/accounts/{address}", srv.handleAccount)

	// ---- Intents / Deals ----
	mux.HandleFunc("GET /api/intents", srv.handleIntents)
	mux.HandleFunc("GET /api/intents/{intentID}", srv.handleIntent)
	mux.HandleFunc("GET /api/intents/{intentID}/shards", srv.handleIntentShards)

	// ---- Miners ----
	mux.HandleFunc("GET /api/miners", srv.handleMiners)
	mux.HandleFunc("GET /api/miners/{address}", srv.handleMiner)

	// ---- Validators ----
	mux.HandleFunc("GET /api/validators", srv.handleValidators)
	mux.HandleFunc("GET /api/validators/{address}", srv.handleValidator)

	// ---- Search ----
	mux.HandleFunc("GET /api/search", srv.handleSearch)

	// ---- Stats ----
	mux.HandleFunc("GET /api/stats/daily", srv.handleDailyStats)

	return mux
}

// ============================================================
// Dashboard
// ============================================================

func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var height, blockCount, txCount, intentCount, minerCount, validatorCount int64
	var totalDataBytes, totalStorageRewards, totalRetrievalRewards, totalSlashed int64
	var activeMiners, activeValidators, finalizedIntents int64

	srv.store.Pool().QueryRow(ctx, "SELECT COALESCE(MAX(height), 0) FROM blocks").Scan(&height)
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM blocks").Scan(&blockCount)
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM transactions").Scan(&txCount)
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM intents").Scan(&intentCount)
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM miners").Scan(&minerCount)
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM validators").Scan(&validatorCount)
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM miners WHERE status = 'active'").Scan(&activeMiners)
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM validators WHERE status = 'active'").Scan(&activeValidators)
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM intents WHERE status IN ('active', 'finalizing')").Scan(&finalizedIntents)
	srv.store.Pool().QueryRow(ctx, "SELECT COALESCE(SUM(file_size), 0) FROM intents WHERE storage_status = 'stored'").Scan(&totalDataBytes)
	srv.store.Pool().QueryRow(ctx, "SELECT COALESCE(SUM(storage_rewards_paid), 0) FROM proof_epochs").Scan(&totalStorageRewards)
	srv.store.Pool().QueryRow(ctx, "SELECT COALESCE(SUM(retrieval_rewards_paid), 0) FROM proof_epochs").Scan(&totalRetrievalRewards)
	srv.store.Pool().QueryRow(ctx, "SELECT COALESCE(SUM(storage_slashed), 0) FROM proof_epochs").Scan(&totalSlashed)

	writeJSON(w, http.StatusOK, map[string]any{
		"height":               height,
		"blocks":               blockCount,
		"transactions":         txCount,
		"intents":              intentCount,
		"finalized_intents":    finalizedIntents,
		"miners":               minerCount,
		"active_miners":        activeMiners,
		"validators":           validatorCount,
		"active_validators":    activeValidators,
		"total_data_bytes":     totalDataBytes,
		"storage_rewards":      totalStorageRewards,
		"retrieval_rewards":    totalRetrievalRewards,
		"total_slashed":        totalSlashed,
	})
}

// ============================================================
// Blocks
// ============================================================

func (srv *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit

	rows, err := srv.store.Pool().Query(ctx,
		"SELECT height, hash, prev_hash, time_unix, tx_count, producer_address, finalized, COALESCE(tx_root, ''), COALESCE(state_root, ''), COALESCE(receipts_root, '') FROM blocks ORDER BY height DESC LIMIT $1 OFFSET $2",
		limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	blocks := make([]map[string]any, 0)
	for rows.Next() {
		var height uint64
		var hash, prevHash, producerAddr, txRoot, stateRoot, receiptsRoot string
		var timeUnix int64
		var txCount int
		var finalized bool
		if err := rows.Scan(&height, &hash, &prevHash, &timeUnix, &txCount, &producerAddr, &finalized, &txRoot, &stateRoot, &receiptsRoot); err != nil {
			continue
		}
		blocks = append(blocks, map[string]any{
			"height":            height,
			"hash":              hash,
			"prev_hash":         prevHash,
			"time_unix":         timeUnix,
			"tx_count":          txCount,
			"producer_address":  producerAddr,
			"finalized":         finalized,
			"tx_root":           txRoot,
			"state_root":        stateRoot,
			"receipts_root":     receiptsRoot,
		})
	}

	var total int64
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM blocks").Scan(&total)

	writeJSON(w, http.StatusOK, map[string]any{
		"blocks": blocks,
		"total":  total,
		"page":   page,
		"limit":  limit,
	})
}

func (srv *Server) handleLatestBlock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var b struct {
		Height          uint64 `json:"height"`
		Hash            string `json:"hash"`
		PrevHash        string `json:"prev_hash"`
		TimeUnix        int64  `json:"time_unix"`
		TxCount         int    `json:"tx_count"`
		ProducerAddress string `json:"producer_address"`
		ProducerPubKey  string `json:"producer_public_key"`
		TxRoot          string `json:"tx_root"`
		StateRoot       string `json:"state_root"`
		ReceiptsRoot    string `json:"receipts_root"`
		Finalized       bool   `json:"finalized"`
		VotingPower     uint64 `json:"voting_power"`
		TotalPower      uint64 `json:"total_power"`
	}
	err := srv.store.Pool().QueryRow(ctx,
		"SELECT height, hash, prev_hash, time_unix, tx_count, producer_address, COALESCE(producer_public_key,''), COALESCE(tx_root,''), COALESCE(state_root,''), COALESCE(receipts_root,''), finalized, COALESCE(voting_power,0), COALESCE(total_power,0) FROM blocks ORDER BY height DESC LIMIT 1",
	).Scan(&b.Height, &b.Hash, &b.PrevHash, &b.TimeUnix, &b.TxCount, &b.ProducerAddress, &b.ProducerPubKey, &b.TxRoot, &b.StateRoot, &b.ReceiptsRoot, &b.Finalized, &b.VotingPower, &b.TotalPower)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (srv *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	param := r.PathValue("heightOrHash")

	var b struct {
		Height          uint64 `json:"height"`
		Hash            string `json:"hash"`
		PrevHash        string `json:"prev_hash"`
		Round           uint64 `json:"round"`
		TimeUnix        int64  `json:"time_unix"`
		TxCount         int    `json:"tx_count"`
		ProducerAddress string `json:"producer_address"`
		ProducerPubKey  string `json:"producer_public_key"`
		TxRoot          string `json:"tx_root"`
		StateRoot       string `json:"state_root"`
		ReceiptsRoot    string `json:"receipts_root"`
		Signature       string `json:"signature"`
		Finalized       bool   `json:"finalized"`
		VotingPower     uint64 `json:"voting_power"`
		TotalPower      uint64 `json:"total_power"`
	}
	var err error

	// try height first
	if height, parseErr := strconv.ParseUint(param, 10, 64); parseErr == nil {
		err = srv.store.Pool().QueryRow(ctx, `
			SELECT height, hash, prev_hash, round, time_unix, tx_count,
				producer_address, COALESCE(producer_public_key,''),
				COALESCE(tx_root,''), COALESCE(state_root,''), COALESCE(receipts_root,''),
				COALESCE(signature,''), finalized,
				COALESCE(voting_power,0), COALESCE(total_power,0)
			FROM blocks WHERE height = $1
		`, height).Scan(&b.Height, &b.Hash, &b.PrevHash, &b.Round, &b.TimeUnix, &b.TxCount,
			&b.ProducerAddress, &b.ProducerPubKey, &b.TxRoot, &b.StateRoot, &b.ReceiptsRoot,
			&b.Signature, &b.Finalized, &b.VotingPower, &b.TotalPower)
	} else {
		err = srv.store.Pool().QueryRow(ctx, `
			SELECT height, hash, prev_hash, round, time_unix, tx_count,
				producer_address, COALESCE(producer_public_key,''),
				COALESCE(tx_root,''), COALESCE(state_root,''), COALESCE(receipts_root,''),
				COALESCE(signature,''), finalized,
				COALESCE(voting_power,0), COALESCE(total_power,0)
			FROM blocks WHERE hash = $1
		`, param).Scan(&b.Height, &b.Hash, &b.PrevHash, &b.Round, &b.TimeUnix, &b.TxCount,
			&b.ProducerAddress, &b.ProducerPubKey, &b.TxRoot, &b.StateRoot, &b.ReceiptsRoot,
			&b.Signature, &b.Finalized, &b.VotingPower, &b.TotalPower)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	// also fetch transactions in this block
	rows, _ := srv.store.Pool().Query(ctx,
		"SELECT tx_id, tx_type, from_address, nonce, fee, payload_hash, payload, block_height, created_at_unix FROM transactions WHERE block_height = $1 ORDER BY tx_id",
		b.Height)
	defer rows.Close()

	txs := make([]map[string]any, 0)
	for rows.Next() {
		var txID, txType, from, payloadHash string
		var nonce, fee uint64
		var payloadJSON []byte
		var blockHeight int64
		var created int64
		rows.Scan(&txID, &txType, &from, &nonce, &fee, &payloadHash, &payloadJSON, &blockHeight, &created)
		var payload any
		json.Unmarshal(payloadJSON, &payload)
		txs = append(txs, map[string]any{
			"tx_id":         txID,
			"type":          txType,
			"from":          from,
			"nonce":         nonce,
			"fee":           fee,
			"payload_hash":  payloadHash,
			"payload":       payload,
			"block_height":  blockHeight,
			"created_at_unix": created,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"block":        b,
		"transactions": txs,
	})
}

// ============================================================
// Transactions
// ============================================================

func (srv *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit

	blockHeight := r.URL.Query().Get("block")
	address := r.URL.Query().Get("address")
	txType := r.URL.Query().Get("type")

	where := "WHERE 1=1"
	args := []any{}
	argIdx := 1

	if blockHeight != "" {
		where += " AND block_height = $" + strconv.Itoa(argIdx)
		args = append(args, blockHeight)
		argIdx++
	}
	if address != "" {
		where += " AND from_address = $" + strconv.Itoa(argIdx)
		args = append(args, address)
		argIdx++
	}
	if txType != "" {
		where += " AND tx_type = $" + strconv.Itoa(argIdx)
		args = append(args, txType)
		argIdx++
	}

	args = append(args, limit, offset)
	query := "SELECT tx_id, tx_type, from_address, nonce, fee, payload_hash, payload, block_height, created_at_unix FROM transactions " + where + " ORDER BY created_at_unix DESC LIMIT $" + strconv.Itoa(argIdx) + " OFFSET $" + strconv.Itoa(argIdx+1)

	rows, err := srv.store.Pool().Query(ctx, query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	txs := make([]map[string]any, 0)
	for rows.Next() {
		var txID, txType, from, payloadHash string
		var nonce, fee uint64
		var payloadJSON []byte
		var blockHeight int64
		var created int64
		rows.Scan(&txID, &txType, &from, &nonce, &fee, &payloadHash, &payloadJSON, &blockHeight, &created)
		var payload any
		json.Unmarshal(payloadJSON, &payload)
		txs = append(txs, map[string]any{
			"tx_id":           txID,
			"type":            txType,
			"from":            from,
			"nonce":           nonce,
			"fee":             fee,
			"payload_hash":    payloadHash,
			"payload":         payload,
			"block_height":    blockHeight,
			"created_at_unix": created,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"transactions": txs,
		"page":         page,
		"limit":        limit,
	})
}

func (srv *Server) handleTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txID := r.PathValue("txID")

	var txIDOut, txType, from, payloadHash string
	var nonce, fee uint64
	var nonceProtected bool
	var payloadJSON []byte
	var blockHeight int64
	var created int64
	var blockHash, producerAddress string
	var blockTime int64

	err := srv.store.Pool().QueryRow(ctx, `
		SELECT t.tx_id, t.tx_type, t.from_address, t.nonce, t.nonce_protected,
			t.fee, t.payload_hash, t.payload, t.block_height, t.created_at_unix,
			COALESCE(b.hash,''), COALESCE(b.producer_address,''), COALESCE(b.time_unix,0)
		FROM transactions t
		LEFT JOIN blocks b ON b.height = t.block_height
		WHERE t.tx_id = $1
	`, txID).Scan(&txIDOut, &txType, &from, &nonce, &nonceProtected, &fee, &payloadHash, &payloadJSON, &blockHeight, &created, &blockHash, &producerAddress, &blockTime)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	var payload any
	json.Unmarshal(payloadJSON, &payload)

	writeJSON(w, http.StatusOK, map[string]any{
		"tx_id":           txIDOut,
		"type":            txType,
		"from":            from,
		"nonce":           nonce,
		"nonce_protected": nonceProtected,
		"fee":             fee,
		"payload_hash":    payloadHash,
		"payload":         payload,
		"block_height":    blockHeight,
		"block_hash":      blockHash,
		"producer":        producerAddress,
		"block_time_unix": blockTime,
		"created_at_unix": created,
	})
}

// ============================================================
// Accounts
// ============================================================

func (srv *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	address := r.PathValue("address")

	var balance, nonce, lockedStake, lockedStorage int64
	err := srv.store.Pool().QueryRow(ctx,
		"SELECT COALESCE(balance,0), COALESCE(nonce,0), COALESCE(locked_stake,0), COALESCE(locked_storage,0) FROM accounts WHERE address = $1",
		address,
	).Scan(&balance, &nonce, &lockedStake, &lockedStorage)

	if err != nil {
		// Try to refresh from chain
		var acc wire.Account
		if cerr := fetchChainAccount(srv.store.chainURL, address, &acc); cerr != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		srv.store.Pool().Exec(ctx, `
			INSERT INTO accounts (address, public_key, balance, nonce, locked_stake, locked_storage)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (address) DO UPDATE SET balance=$3, nonce=$4
		`, acc.Address, acc.PublicKey, int64(acc.Balance), int64(acc.Nonce), int64(acc.LockedStake), int64(acc.LockedStorage))
		balance = int64(acc.Balance)
		nonce = int64(acc.Nonce)
		lockedStake = int64(acc.LockedStake)
		lockedStorage = int64(acc.LockedStorage)
	}

	// get transaction count
	var txCount int64
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE from_address = $1", address).Scan(&txCount)

	writeJSON(w, http.StatusOK, map[string]any{
		"address":        address,
		"balance":        balance,
		"nonce":          nonce,
		"locked_stake":   lockedStake,
		"locked_storage": lockedStorage,
		"tx_count":       txCount,
	})
}

// ============================================================
// Intents
// ============================================================

func (srv *Server) handleIntents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit
	status := r.URL.Query().Get("status")
	user := r.URL.Query().Get("user")

	where := "WHERE 1=1"
	args := []any{}
	argIdx := 1
	if status != "" {
		where += " AND status = $" + strconv.Itoa(argIdx)
		args = append(args, status)
		argIdx++
	}
	if user != "" {
		where += " AND user_address = $" + strconv.Itoa(argIdx)
		args = append(args, user)
		argIdx++
	}
	args = append(args, limit, offset)

	rows, err := srv.store.Pool().Query(ctx,
		"SELECT intent_id, user_address, file_name, file_size, status, storage_status, deal_id, locked_fee, paid_fee, uploaded_size, expires_at_unix FROM intents "+where+" ORDER BY intent_id DESC LIMIT $"+strconv.Itoa(argIdx)+" OFFSET $"+strconv.Itoa(argIdx+1),
		args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	intents := make([]map[string]any, 0)
	for rows.Next() {
		var intentID, user, fileName, status, storageStatus, dealID string
		var fileSize, lockedFee, paidFee, uploadedSize int64
		var expiresAt int64
		rows.Scan(&intentID, &user, &fileName, &fileSize, &status, &storageStatus, &dealID, &lockedFee, &paidFee, &uploadedSize, &expiresAt)
		intents = append(intents, map[string]any{
			"intent_id":      intentID,
			"user":           user,
			"file_name":      fileName,
			"file_size":      fileSize,
			"status":         status,
			"storage_status": storageStatus,
			"deal_id":        dealID,
			"locked_fee":     lockedFee,
			"paid_fee":       paidFee,
			"uploaded_size":  uploadedSize,
			"expires_at":     expiresAt,
		})
	}

	var total int64
	srv.store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM intents "+where).Scan(&total)

	writeJSON(w, http.StatusOK, map[string]any{
		"intents": intents,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}

func (srv *Server) handleIntent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	intentID := r.PathValue("intentID")

	var intent wire.IntentView
	var erasureJSON, encryptionJSON []byte
	err := srv.store.Pool().QueryRow(ctx, `
		SELECT intent_id, user_address, file_name, file_size, segment_size, file_root,
			deal_id, status, storage_status, access_status, moderation_status,
			locked_fee, paid_fee, refunded_fee, uploaded_size, committed_segments,
			data_shards, parity_shards, shard_size, policy_class, policy_duration, policy_redundancy,
			erasure, encryption, expires_at_unix, terminated_at_unix
		FROM intents WHERE intent_id = $1
	`, intentID).Scan(
		&intent.IntentID, &intent.User, &intent.FileName, &intent.FileSize,
		&intent.SegmentSize, &intent.FileRoot, &intent.DealID,
		&intent.Status, &intent.StorageStatus, &intent.AccessStatus, &intent.ModerationStatus,
		&intent.LockedFee, &intent.PaidFee, &intent.RefundedFee,
		&intent.UploadedSize, &intent.CommittedSegments,
		&intent.Erasure.DataShards, &intent.Erasure.ParityShards, &intent.Erasure.ShardSize,
		&intent.Policy.Class, &intent.Policy.Duration, &intent.Policy.Redundancy,
		&erasureJSON, &encryptionJSON, &intent.ExpiresAtUnix, &intent.TerminatedAtUnix,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, intent)
}

func (srv *Server) handleIntentShards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	intentID := r.PathValue("intentID")

	rows, err := srv.store.Pool().Query(ctx,
		"SELECT segment_id, shard_index, miner_address, miner_endpoint, shard_hash, shard_cid, shard_size, committed FROM shard_assignments WHERE intent_id = $1 ORDER BY segment_id, shard_index",
		intentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	shards := make([]map[string]any, 0)
	for rows.Next() {
		var segID, shardIdx int
		var minerAddr, endpoint, shardHash, shardCID string
		var shardSize int64
		var committed bool
		rows.Scan(&segID, &shardIdx, &minerAddr, &endpoint, &shardHash, &shardCID, &shardSize, &committed)
		shards = append(shards, map[string]any{
			"segment_id":     segID,
			"shard_index":    shardIdx,
			"miner_address":  minerAddr,
			"endpoint":       endpoint,
			"shard_hash":     shardHash,
			"shard_cid":      shardCID,
			"shard_size":     shardSize,
			"committed":      committed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"shards": shards})
}

// ============================================================
// Miners
// ============================================================

func (srv *Server) handleMiners(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := srv.store.Pool().Query(ctx,
		"SELECT miner_address, public_key, endpoint, capacity_bytes, used_bytes, stake, status, proof_success, proof_failure, retrieval_success, retrieval_bytes, storage_rewards, retrieval_rewards, repair_rewards, slashed FROM miners ORDER BY capacity_bytes DESC LIMIT 100")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	miners := make([]map[string]any, 0)
	for rows.Next() {
		m := map[string]any{}
		var addr, pubKey, endpoint, status string
		var cap, used, stake int64
		var proofOK, proofFail, retOK, retBytes, sRew, rRew, repRew, sl int64
		rows.Scan(&addr, &pubKey, &endpoint, &cap, &used, &stake, &status, &proofOK, &proofFail, &retOK, &retBytes, &sRew, &rRew, &repRew, &sl)
		m["miner_address"] = addr
		m["public_key"] = pubKey
		m["endpoint"] = endpoint
		m["capacity_bytes"] = cap
		m["used_bytes"] = used
		m["stake"] = stake
		m["status"] = status
		m["proof_success"] = proofOK
		m["proof_failure"] = proofFail
		m["retrieval_success"] = retOK
		m["retrieval_bytes"] = retBytes
		m["storage_rewards"] = sRew
		m["retrieval_rewards"] = rRew
		m["repair_rewards"] = repRew
		m["slashed"] = sl
		miners = append(miners, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"miners": miners})
}

func (srv *Server) handleMiner(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	address := r.PathValue("address")

	var addr, pubKey, endpoint, status string
	var cap, used, reserved, stake int64
	var proofOK, proofFail, consFails, rewards, sRewards, retOK, retBytes, retRewards, repRewards, slashed int64
	var effWeight, speedScore, antiSpam, registeredAt, exitedAt, lastSeen int64

	err := srv.store.Pool().QueryRow(ctx, `
		SELECT miner_address, public_key, endpoint, capacity_bytes, used_bytes, reserved_bytes, stake, status,
			proof_success, proof_failure, consecutive_failures, rewards, storage_rewards,
			retrieval_success, retrieval_bytes, retrieval_rewards, repair_rewards, slashed,
			effective_weight, speed_score, anti_spam_score, registered_at_unix, exited_at_unix, last_seen_unix
		FROM miners WHERE miner_address = $1
	`, address).Scan(&addr, &pubKey, &endpoint, &cap, &used, &reserved, &stake, &status,
		&proofOK, &proofFail, &consFails, &rewards, &sRewards,
		&retOK, &retBytes, &retRewards, &repRewards, &slashed,
		&effWeight, &speedScore, &antiSpam, &registeredAt, &exitedAt, &lastSeen)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"miner_address":         addr,
		"public_key":            pubKey,
		"endpoint":              endpoint,
		"capacity_bytes":        cap,
		"used_bytes":            used,
		"reserved_bytes":        reserved,
		"stake":                 stake,
		"status":                status,
		"proof_success":         proofOK,
		"proof_failure":         proofFail,
		"consecutive_failures":  consFails,
		"rewards":               rewards,
		"storage_rewards":       sRewards,
		"retrieval_success":     retOK,
		"retrieval_bytes":       retBytes,
		"retrieval_rewards":     retRewards,
		"repair_rewards":        repRewards,
		"slashed":               slashed,
		"effective_weight":      effWeight,
		"speed_score":           speedScore,
		"anti_spam_score":       antiSpam,
		"registered_at_unix":    registeredAt,
		"exited_at_unix":        exitedAt,
		"last_seen_unix":        lastSeen,
	})
}

// ============================================================
// Validators
// ============================================================

func (srv *Server) handleValidators(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := srv.store.Pool().Query(ctx,
		"SELECT validator_address, public_key, stake, delegated_stake, status, consensus, produced_blocks, slashed, evidence_count, rewards FROM validators ORDER BY stake DESC LIMIT 100")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	validators := make([]map[string]any, 0)
	for rows.Next() {
		var addr, pubKey, status string
		var stake, delStake, prodBlocks, slash, evidence, rewards int64
		var consensus bool
		rows.Scan(&addr, &pubKey, &stake, &delStake, &status, &consensus, &prodBlocks, &slash, &evidence, &rewards)
		validators = append(validators, map[string]any{
			"validator_address": addr,
			"public_key":        pubKey,
			"stake":             stake,
			"delegated_stake":   delStake,
			"status":            status,
			"consensus":         consensus,
			"produced_blocks":   prodBlocks,
			"slashed":           slash,
			"evidence_count":    evidence,
			"rewards":           rewards,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"validators": validators})
}

func (srv *Server) handleValidator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	address := r.PathValue("address")

	var addr, pubKey, endpoint, status string
	var stake, delStake, selfStake int64
	var consensus bool
	var prodBlocks, slashed, evidence, delegCount, rewards, delRewards, registeredAt int64

	err := srv.store.Pool().QueryRow(ctx, `
		SELECT validator_address, public_key, endpoint, stake, delegated_stake, self_stake, status,
			consensus, produced_blocks, slashed, evidence_count, delegator_count, rewards, delegation_rewards, registered_at_unix
		FROM validators WHERE validator_address = $1
	`, address).Scan(&addr, &pubKey, &endpoint, &stake, &delStake, &selfStake, &status,
		&consensus, &prodBlocks, &slashed, &evidence, &delegCount, &rewards, &delRewards, &registeredAt)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"validator_address":   addr,
		"public_key":          pubKey,
		"endpoint":            endpoint,
		"stake":               stake,
		"delegated_stake":     delStake,
		"self_stake":          selfStake,
		"status":              status,
		"consensus":           consensus,
		"produced_blocks":     prodBlocks,
		"slashed":             slashed,
		"evidence_count":      evidence,
		"delegator_count":     delegCount,
		"rewards":             rewards,
		"delegation_rewards":  delRewards,
		"registered_at_unix":  registeredAt,
	})
}

// ============================================================
// Search
// ============================================================

func (srv *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}

	results := make([]map[string]any, 0)

	// search blocks by height
	if h, err := strconv.ParseUint(q, 10, 64); err == nil {
		var hash string
		var timeUnix int64
		if err := srv.store.Pool().QueryRow(ctx, "SELECT hash, time_unix FROM blocks WHERE height = $1", h).Scan(&hash, &timeUnix); err == nil {
			results = append(results, map[string]any{
				"type": "block", "height": h, "hash": hash, "time_unix": timeUnix,
			})
		}
	}

	// search blocks by hash (partial)
	rows, _ := srv.store.Pool().Query(ctx, "SELECT height, hash, time_unix FROM blocks WHERE hash LIKE $1 LIMIT 3", "%"+q+"%")
	if rows != nil {
		for rows.Next() {
			var h uint64
			var hash string
			var t int64
			rows.Scan(&h, &hash, &t)
			results = append(results, map[string]any{
				"type": "block", "height": h, "hash": hash, "time_unix": t,
			})
		}
		rows.Close()
	}

	// search transactions
	txRows, _ := srv.store.Pool().Query(ctx, "SELECT tx_id, tx_type, from_address, block_height FROM transactions WHERE tx_id = $1 OR from_address = $1 OR tx_id LIKE $2 LIMIT 5", q, "%"+q+"%")
	if txRows != nil {
		for txRows.Next() {
			var txID, txType, from string
			var bh int64
			txRows.Scan(&txID, &txType, &from, &bh)
			results = append(results, map[string]any{
				"type": "transaction", "tx_id": txID, "tx_type": txType, "from": from, "block_height": bh,
			})
		}
		txRows.Close()
	}

	// search intents
	intentRows, _ := srv.store.Pool().Query(ctx,
		"SELECT intent_id, file_name, user_address, status FROM intents WHERE intent_id = $1 OR file_name ILIKE $2 OR user_address = $1 LIMIT 5",
		q, "%"+q+"%")
	if intentRows != nil {
		for intentRows.Next() {
			var id, fname, user, status string
			intentRows.Scan(&id, &fname, &user, &status)
			results = append(results, map[string]any{
				"type": "intent", "intent_id": id, "file_name": fname, "user": user, "status": status,
			})
		}
		intentRows.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// ============================================================
// Stats
// ============================================================

func (srv *Server) handleDailyStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := srv.store.Pool().Query(ctx,
		"SELECT date, tx_count, blocks_produced, new_intents, finalized_intents FROM daily_stats ORDER BY date DESC LIMIT 30")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	stats := make([]map[string]any, 0)
	for rows.Next() {
		var date time.Time
		var txCount, blocks, newIntents, finIntents int64
		rows.Scan(&date, &txCount, &blocks, &newIntents, &finIntents)
		stats = append(stats, map[string]any{
			"date":              date.Format("2006-01-02"),
			"tx_count":          txCount,
			"blocks_produced":   blocks,
			"new_intents":       newIntents,
			"finalized_intents": finIntents,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"daily_stats": stats})
}

// ============================================================
// Helpers
// ============================================================

func fetchChainAccount(chainURL, address string, out *wire.Account) error {
	return client.NewHTTP(chainURL).Get("/accounts/"+address, out)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
