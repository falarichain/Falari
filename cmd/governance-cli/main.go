package main

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"chain/internal/client"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

const defaultChainURL = "http://localhost:8081"

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "keygen":
		keygen(os.Args[2:])
	case "propose":
		propose(os.Args[2:])
	case "vote":
		vote(os.Args[2:])
	case "execute":
		execute(os.Args[2:])
	case "proposals":
		proposals(os.Args[2:])
	case "operators":
		operators(os.Args[2:])
	case "audit":
		audit(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

// ── Key Management ──

type governanceKeyFile struct {
	Address    string `json:"address"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

func loadGovernanceKey(path string) governanceKeyFile {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("failed to read key file: %v", err)
	}
	var key governanceKeyFile
	if err := json.Unmarshal(data, &key); err != nil {
		log.Fatalf("failed to parse key file: %v", err)
	}
	return key
}

func loadGovernanceECDSAKey(path string) *ecdsa.PrivateKey {
	keyFile := loadGovernanceKey(path)
	privateKey, err := ethcrypto.HexToECDSA(trimHexPrefix(keyFile.PrivateKey))
	if err != nil {
		log.Fatalf("failed to parse ECDSA private key: %v", err)
	}
	return privateKey
}

// ── Commands ──

func keygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "output key file path")
	fs.Parse(args)

	if *out == "" {
		log.Fatal("-out is required")
	}
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		log.Fatalf("failed to generate key: %v", err)
	}
	keyFile := governanceKeyFile{
		Address:    wire.AccountAddress(&privateKey.PublicKey),
		PublicKey:  encodeHex(ethcrypto.FromECDSAPub(&privateKey.PublicKey)),
		PrivateKey: encodeHex(ethcrypto.FromECDSA(privateKey)),
	}
	data, err := json.MarshalIndent(keyFile, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal key: %v", err)
	}
	if err := os.WriteFile(*out, data, 0600); err != nil {
		log.Fatalf("failed to write key file: %v", err)
	}
	fmt.Printf("governance key generated: address=%s public_key=%s\n", keyFile.Address, keyFile.PublicKey)
}

func propose(args []string) {
	fs := flag.NewFlagSet("propose", flag.ExitOnError)
	chainURL := fs.String("chain", defaultChainURL, "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file")
	intentID := fs.String("intent", "", "intent id (required for deal actions)")
	action := fs.String("action", "freeze", "action: freeze, block, legal_hold, appeal, add_operator, remove_operator, update_operator, update_config, update_mining_params, update_fee_market")
	reasonHash := fs.String("reason-hash", "", "required reason hash")
	expiresAtUnix := fs.Int64("expires-at-unix", 0, "future unix timestamp for freeze expiration")
	preserveStorage := fs.Bool("preserve-storage", true, "keep storage responsibility active")
	appealDeadlineUnix := fs.Int64("appeal-deadline-unix", 0, "appeal deadline unix timestamp")
	targetOperator := fs.String("target-operator", "", "target operator address (for add/remove/update_operator)")
	targetPublicKey := fs.String("target-public-key", "", "target operator public key (for add/update_operator)")
	targetPermissions := fs.String("target-permissions", "", "comma-separated permissions (for add/update_operator)")
	dataModThresholdNum := fs.Int("data-mod-threshold-num", 0, "data moderation threshold numerator (for update_config)")
	dataModThresholdDen := fs.Int("data-mod-threshold-den", 0, "data moderation threshold denominator (for update_config)")
	opChangeThresholdNum := fs.Int("op-change-threshold-num", 0, "operator change threshold numerator (for update_config)")
	opChangeThresholdDen := fs.Int("op-change-threshold-den", 0, "operator change threshold denominator (for update_config)")
	// Mining params flags.
	storageReleaseRateBPS := fs.Uint64("storage-release-rate-bps", 0, "storage pool release rate BPS (for update_mining_params)")
	storageRewardPerBlock := fs.Uint64("storage-reward-per-block", 0, "storage pool per-block reward in smallest units (for update_mining_params)")
	foundationRewardPerBlock := fs.Uint64("foundation-reward-per-block", 0, "foundation pool per-block reward in smallest units (for update_mining_params)")
	retrievalRewardPerBlock := fs.Uint64("retrieval-reward-per-block", 0, "retrieval pool per-block reward in smallest units (for update_mining_params)")
	retrievalReleaseRateBPS := fs.Uint64("retrieval-release-rate-bps", 0, "retrieval pool release rate BPS (for update_mining_params)")
	storedBytesWeightBPS := fs.Uint64("stored-bytes-weight-bps", 0, "stored bytes weight factor BPS (for update_mining_params)")
	proofScoreWeightBPS := fs.Uint64("proof-score-weight-bps", 0, "proof score weight factor BPS (for update_mining_params)")
	availabilityWeightBPS := fs.Uint64("availability-weight-bps", 0, "availability weight factor BPS (for update_mining_params)")
	retrievalSpeedWeightBPS := fs.Uint64("retrieval-speed-weight-bps", 0, "retrieval speed weight factor BPS (for update_mining_params)")
	ipDispersionWeightBPS := fs.Uint64("ip-dispersion-weight-bps", 0, "IP dispersion weight factor BPS (for update_mining_params)")
	retrievalRewardPerMiB := fs.Uint64("retrieval-reward-per-mib", 0, "retrieval reward per MiB (for update_mining_params)")
	maxRetrievalRewardPerWindow := fs.Uint64("max-retrieval-reward-per-window", 0, "max retrieval reward per window (for update_mining_params)")
	minerDegradeThreshold := fs.Uint64("miner-degrade-threshold", 0, "miner degrade threshold (for update_mining_params)")
	storageProofSamples := fs.Int("storage-proof-samples", 0, "storage proof samples count (for update_mining_params)")
	validatorCommissionBPS := fs.Uint64("validator-commission-bps", 0, "validator commission BPS (for update_mining_params)")
	retrievalWeightBPS := fs.Uint64("retrieval-weight-bps", 0, "retrieval weight BPS (for update_mining_params)")
	targetBlockBytes := fs.Uint64("target-block-bytes", 0, "target block size in bytes (for update_mining_params)")
	maxBlockBytes := fs.Uint64("max-block-bytes", 0, "max block size in bytes (for update_mining_params)")
	maxBlockTxs := fs.Uint64("max-block-txs", 0, "max transactions per block (for update_mining_params)")
	maxTxBytes := fs.Uint64("max-tx-bytes", 0, "max regular transaction size in bytes (for update_mining_params)")
	maxStorageTxBytes := fs.Uint64("max-storage-tx-bytes", 0, "max storage metadata transaction size in bytes (for update_mining_params)")
	registrationBonusAmount := fs.Uint64("registration-bonus-amount", 0, "registration bonus amount in smallest units (for update_mining_params)")
	minBonusProofCount := fs.Uint64("min-bonus-proof-count", 0, "minimum successful proofs to release bonus (for update_mining_params)")
	minBonusSuccessRateBPS := fs.Uint64("min-bonus-success-rate-bps", 0, "minimum success rate BPS for bonus release (for update_mining_params)")
	minBonusRetrievalCount := fs.Uint64("min-bonus-retrieval-count", 0, "minimum successful retrievals for bonus release (for update_mining_params)")
	maxBonusAddresses := fs.Uint64("max-bonus-addresses", 0, "maximum miners who can receive bonus (for update_mining_params)")
	bonusDeadlineSeconds := fs.Uint64("bonus-deadline-seconds", 0, "bonus deadline in seconds (for update_mining_params)")
	activationWindowSeconds := fs.Uint64("activation-window-seconds", 0, "activation window in seconds (for update_mining_params)")
	// Fee market flags.
	feeMarketBaseFee := fs.Uint64("fee-market-base-fee", 0, "base fee in smallest units (for update_fee_market)")
	feeMarketTargetBlockTxs := fs.Int("fee-market-target-block-txs", 0, "target transactions per block (for update_fee_market)")
	feeMultiplierBridgeOut := fs.Uint64("fee-multiplier-bridge-out", 0, "bridge_out fee multiplier BPS, 10000=1.0x (for update_fee_market)")
	feeMultiplierCreateIntent := fs.Uint64("fee-multiplier-create-intent", 0, "create_intent fee multiplier BPS (for update_fee_market)")
	feeMultiplierUploadNFT := fs.Uint64("fee-multiplier-upload-nft", 0, "upload_nft_template fee multiplier BPS (for update_fee_market)")
	feeMultiplierRegisterVal := fs.Uint64("fee-multiplier-register-validator", 0, "register_validator fee multiplier BPS (for update_fee_market)")
	feeMultiplierBatchCommit := fs.Uint64("fee-multiplier-batch-commit", 0, "batch_commit fee multiplier BPS (for update_fee_market)")
	fs.Parse(args)

	if *keyPath == "" || *reasonHash == "" {
		log.Fatal("-key and -reason-hash are required")
	}

	isOpMgmt := *action == "add_operator" || *action == "remove_operator" || *action == "update_operator"
	isConfig := *action == "update_config"
	isMiningParams := *action == "update_mining_params"
	isFeeMarket := *action == "update_fee_market"
	if !isOpMgmt && !isConfig && !isMiningParams && !isFeeMarket && *intentID == "" {
		log.Fatal("-intent is required for deal actions")
	}

	key := loadGovernanceKey(*keyPath)
	privKey := loadGovernanceECDSAKey(*keyPath)

	var permissions []string
	if *targetPermissions != "" {
		for _, p := range strings.Split(*targetPermissions, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				permissions = append(permissions, p)
			}
		}
	}

	now := time.Now().Unix()
	req := wire.CreateGovernanceProposalRequest{
		Proposer:                          key.Address,
		IntentID:                          *intentID,
		Action:                            *action,
		ReasonHash:                        *reasonHash,
		ExpiresAtUnix:                     *expiresAtUnix,
		PreserveStorage:                   *preserveStorage,
		AppealDeadlineUnix:                *appealDeadlineUnix,
		TargetOperator:                    *targetOperator,
		TargetPublicKey:                   *targetPublicKey,
		TargetPermissions:                 permissions,
		TargetDataModerationThresholdNum:  *dataModThresholdNum,
		TargetDataModerationThresholdDen:  *dataModThresholdDen,
		TargetOperatorChangeThresholdNum:  *opChangeThresholdNum,
		TargetOperatorChangeThresholdDen:  *opChangeThresholdDen,
		TargetStorageReleaseRateBPS:       *storageReleaseRateBPS,
		TargetStorageRewardPerBlock:       *storageRewardPerBlock,
		TargetFoundationRewardPerBlock:    *foundationRewardPerBlock,
		TargetRetrievalRewardPerBlock:     *retrievalRewardPerBlock,
		TargetRetrievalReleaseRateBPS:     *retrievalReleaseRateBPS,
		TargetStoredBytesWeightBPS:        *storedBytesWeightBPS,
		TargetProofScoreWeightBPS:         *proofScoreWeightBPS,
		TargetAvailabilityWeightBPS:       *availabilityWeightBPS,
		TargetRetrievalSpeedWeightBPS:     *retrievalSpeedWeightBPS,
		TargetIPDispersionWeightBPS:       *ipDispersionWeightBPS,
		TargetRetrievalRewardPerMiB:       *retrievalRewardPerMiB,
		TargetMaxRetrievalRewardPerWindow: *maxRetrievalRewardPerWindow,
		TargetMinerDegradeThreshold:       *minerDegradeThreshold,
		TargetStorageProofSamples:         *storageProofSamples,
		TargetValidatorCommissionBPS:      *validatorCommissionBPS,
		TargetRetrievalWeightBPS:          *retrievalWeightBPS,
		TargetBlockBytes:                  *targetBlockBytes,
		TargetMaxBlockBytes:               *maxBlockBytes,
		TargetMaxBlockTxs:                 *maxBlockTxs,
		TargetMaxTxBytes:                  *maxTxBytes,
		TargetMaxStorageTxBytes:           *maxStorageTxBytes,
		TargetRegistrationBonusAmount:     *registrationBonusAmount,
		TargetMinBonusProofCount:          *minBonusProofCount,
		TargetMinBonusSuccessRateBPS:      *minBonusSuccessRateBPS,
		TargetMinBonusRetrievalCount:      *minBonusRetrievalCount,
		TargetMaxBonusAddresses:           *maxBonusAddresses,
		TargetBonusDeadlineSeconds:        *bonusDeadlineSeconds,
		TargetActivationWindowSeconds:     *activationWindowSeconds,
		TargetFeeMarketBaseFee:            *feeMarketBaseFee,
		TargetFeeMarketTargetBlockTxs:     *feeMarketTargetBlockTxs,
		TargetFeeMultiplierBridgeOut:      *feeMultiplierBridgeOut,
		TargetFeeMultiplierCreateIntent:   *feeMultiplierCreateIntent,
		TargetFeeMultiplierUploadNFT:      *feeMultiplierUploadNFT,
		TargetFeeMultiplierRegisterVal:    *feeMultiplierRegisterVal,
		TargetFeeMultiplierBatchCommit:    *feeMultiplierBatchCommit,
		Nonce:                             fetchOperatorNonce(*chainURL, key.Address),
		CreatedAtUnix:                     now,
	}
	if err := wire.SignGovernanceProposal(&req, privKey); err != nil {
		log.Fatalf("failed to sign proposal: %v", err)
	}
	var resp wire.CreateGovernanceProposalResponse
	if err := client.NewHTTP(*chainURL).Post("/governance/proposals", req, &resp); err != nil {
		log.Fatal(err)
	}
	if isOpMgmt {
		fmt.Printf("proposal created: id=%s action=%s target=%s status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action, resp.Proposal.TargetOperator, resp.Proposal.Status)
	} else if isConfig {
		fmt.Printf("proposal created: id=%s action=%s data_mod=%d/%d op_change=%d/%d status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action,
			resp.Proposal.TargetDataModerationThresholdNum, resp.Proposal.TargetDataModerationThresholdDen,
			resp.Proposal.TargetOperatorChangeThresholdNum, resp.Proposal.TargetOperatorChangeThresholdDen,
			resp.Proposal.Status)
	} else if isMiningParams {
		fmt.Printf("proposal created: id=%s action=%s status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action, resp.Proposal.Status)
	} else if isFeeMarket {
		fmt.Printf("proposal created: id=%s action=%s base_fee=%d target_block_txs=%d status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action,
			resp.Proposal.TargetFeeMarketBaseFee, resp.Proposal.TargetFeeMarketTargetBlockTxs,
			resp.Proposal.Status)
	} else {
		fmt.Printf("proposal created: id=%s action=%s intent=%s status=%s\n",
			resp.Proposal.ProposalID, resp.Proposal.Action, resp.Proposal.IntentID, resp.Proposal.Status)
	}
}

func vote(args []string) {
	fs := flag.NewFlagSet("vote", flag.ExitOnError)
	chainURL := fs.String("chain", defaultChainURL, "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file")
	proposalID := fs.String("proposal", "", "proposal id")
	approve := fs.Bool("approve", true, "approve or reject the proposal")
	fs.Parse(args)

	if *keyPath == "" || *proposalID == "" {
		log.Fatal("-key and -proposal are required")
	}
	key := loadGovernanceKey(*keyPath)
	privKey := loadGovernanceECDSAKey(*keyPath)

	now := time.Now().Unix()
	req := wire.CastGovernanceVoteRequest{
		ProposalID:    *proposalID,
		Voter:         key.Address,
		Approve:       *approve,
		Nonce:         fetchOperatorNonce(*chainURL, key.Address),
		CreatedAtUnix: now,
	}
	if err := wire.SignGovernanceVote(&req, privKey); err != nil {
		log.Fatalf("failed to sign vote: %v", err)
	}
	var resp wire.CastGovernanceVoteResponse
	if err := client.NewHTTP(*chainURL).Post("/governance/votes", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("vote cast: proposal=%s voter=%s approve=%v approvals=%d rejects=%d threshold=%d executed=%v\n",
		resp.Vote.ProposalID, resp.Vote.Voter, resp.Vote.Approve, resp.ApproveCount, resp.RejectCount, resp.Threshold, resp.Executed)
}

func execute(args []string) {
	fs := flag.NewFlagSet("execute", flag.ExitOnError)
	chainURL := fs.String("chain", defaultChainURL, "chain node URL")
	keyPath := fs.String("key", "", "path to governance operator key file")
	proposalID := fs.String("proposal", "", "proposal id")
	fs.Parse(args)

	if *keyPath == "" || *proposalID == "" {
		log.Fatal("-key and -proposal are required")
	}
	key := loadGovernanceKey(*keyPath)
	privKey := loadGovernanceECDSAKey(*keyPath)

	now := time.Now().Unix()
	req := wire.ExecuteGovernanceProposalRequest{
		ProposalID:    *proposalID,
		Executor:      key.Address,
		Nonce:         fetchOperatorNonce(*chainURL, key.Address),
		CreatedAtUnix: now,
	}
	if err := wire.SignGovernanceExecute(&req, privKey); err != nil {
		log.Fatalf("failed to sign execute request: %v", err)
	}
	var resp wire.ExecuteGovernanceProposalResponse
	if err := client.NewHTTP(*chainURL).Post("/governance/execute", req, &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("proposal executed: id=%s status=%s action=%s access=%s moderation=%s storage=%s\n",
		resp.Proposal.ProposalID, resp.Proposal.Status, resp.Proposal.Action,
		resp.GovernanceResult.AccessStatus, resp.GovernanceResult.ModerationStatus, resp.GovernanceResult.StorageStatus)
}

func proposals(args []string) {
	fs := flag.NewFlagSet("proposals", flag.ExitOnError)
	chainURL := fs.String("chain", defaultChainURL, "chain node URL")
	status := fs.String("status", "", "filter by status: pending, executed, rejected, expired, cancelled")
	intentID := fs.String("intent", "", "filter by intent id")
	fs.Parse(args)

	var resp wire.GovernanceProposalListResponse
	query := url.Values{}
	if *status != "" {
		query.Set("status", *status)
	}
	if *intentID != "" {
		query.Set("intent", *intentID)
	}
	path := "/governance/proposals"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	for _, p := range resp.Proposals {
		votes := resp.Votes[p.ProposalID]
		fmt.Printf("proposal=%s proposer=%s action=%s intent=%s status=%s votes=%d created=%d\n",
			p.ProposalID, p.Proposer, p.Action, p.IntentID, p.Status, len(votes), p.CreatedAtUnix)
		for _, v := range votes {
			fmt.Printf("  vote: voter=%s approve=%v at=%d\n", v.Voter, v.Approve, v.CreatedAtUnix)
		}
	}
}

func operators(args []string) {
	fs := flag.NewFlagSet("operators", flag.ExitOnError)
	chainURL := fs.String("chain", defaultChainURL, "chain node URL")
	fs.Parse(args)

	var resp wire.GovernanceOperatorListResponse
	if err := client.NewHTTP(*chainURL).Get("/governance/operators", &resp); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("data moderation threshold: %d/%d (num=%d den=%d)\n",
		resp.DataModerationThreshold, len(resp.Operators), resp.DataModerationThresholdNum, resp.DataModerationThresholdDen)
	fmt.Printf("operator change threshold: %d/%d (num=%d den=%d)\n\n",
		resp.OperatorChangeThreshold, len(resp.Operators), resp.OperatorChangeThresholdNum, resp.OperatorChangeThresholdDen)
	for _, op := range resp.Operators {
		hasKey := "no"
		if op.PublicKey != "" {
			hasKey = "yes"
		}
		fmt.Printf("operator=%s enabled=%v key=%s nonce=%d permissions=%v\n",
			op.Operator, op.Enabled, hasKey, op.Nonce, op.Permissions)
	}
}

func audit(args []string) {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	chainURL := fs.String("chain", defaultChainURL, "chain node URL")
	intentID := fs.String("intent", "", "intent id")
	operator := fs.String("operator", "", "operator identity")
	action := fs.String("action", "", "governance action")
	fs.Parse(args)

	var resp wire.GovernanceAuditResponse
	query := url.Values{}
	if *intentID != "" {
		query.Set("intent", *intentID)
	}
	if *operator != "" {
		query.Set("operator", *operator)
	}
	if *action != "" {
		query.Set("action", *action)
	}
	path := "/intents/governance/audit"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if err := client.NewHTTP(*chainURL).Get(path, &resp); err != nil {
		log.Fatal(err)
	}
	for _, record := range resp.Records {
		fmt.Printf("audit=%s intent=%s operator=%s action=%s type=%s access=%s moderation=%s storage=%s expires=%d appeal=%d at=%d\n",
			record.AuditID, record.IntentID, record.Operator, record.Action, record.GovernanceType, record.AccessStatus, record.ModerationStatus, record.StorageStatus, record.ModerationExpiresAtUnix, record.AppealDeadlineUnix, record.RecordedAtUnix)
	}
}

// ── Helpers ──

func fetchOperatorNonce(chainURL, address string) uint64 {
	var resp wire.GovernanceOperatorListResponse
	if err := client.NewHTTP(chainURL).Get("/governance/operators", &resp); err != nil {
		log.Fatalf("failed to fetch operator nonce: %v", err)
	}
	for _, op := range resp.Operators {
		if strings.EqualFold(op.Operator, address) {
			return op.Nonce
		}
	}
	log.Fatalf("operator %s not found", address)
	return 0
}

func encodeHex(raw []byte) string {
	return "0x" + hex.EncodeToString(raw)
}

func trimHexPrefix(raw string) string {
	return strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X")
}

func formatGF(amount uint64) string {
	whole := amount / wire.TokenUnit
	frac := amount % wire.TokenUnit
	if frac == 0 {
		return fmt.Sprintf("%d GF", whole)
	}
	s := fmt.Sprintf("%d.%08d", whole, frac)
	s = strings.TrimRight(s, "0")
	return s + " GF"
}

// postOperator is kept for reference by other tools that use operator-authenticated POST.
// Not used directly in governance-cli commands but available for future extension.
func postOperator(chainURL string, path string, in any, out any, keyPath string) error {
	keyFile := loadGovernanceKey(keyPath)
	privateKey := loadGovernanceECDSAKey(keyPath)
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	nonce := fetchOperatorNonce(chainURL, keyFile.Address)
	timestamp := time.Now().Unix()
	cid := chainIDFromStatus(chainURL)
	signature, err := wire.SignOperatorRequest(cid, http.MethodPost, path, raw, nonce, timestamp, privateKey)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(chainURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Operator-Address", keyFile.Address)
	req.Header.Set("X-Operator-Nonce", fmt.Sprintf("%d", nonce))
	req.Header.Set("X-Operator-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Operator-Signature", signature)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func chainIDFromStatus(chainURL string) string {
	var status wire.ChainStatusResponse
	if err := client.NewHTTP(chainURL).Get("/status", &status); err != nil {
		log.Fatal(err)
	}
	if status.ChainID == "" {
		log.Fatal("chain status did not include chain_id")
	}
	return status.ChainID
}

func usage() {
	fmt.Println(`usage:
  governance-cli keygen    -out ./governance-key.json
  governance-cli propose   -chain http://localhost:8081 -key ./key.json -intent intent_xxx -action freeze -reason-hash hash
  governance-cli propose   -chain http://localhost:8081 -key ./key.json -action add_operator -reason-hash hash -target-operator addr -target-public-key key -target-permissions freeze,block
  governance-cli vote      -chain http://localhost:8081 -key ./key.json -proposal gov_proposal_xxx -approve
  governance-cli execute   -chain http://localhost:8081 -key ./key.json -proposal gov_proposal_xxx
  governance-cli proposals -chain http://localhost:8081 -status pending
  governance-cli operators -chain http://localhost:8081
  governance-cli audit     -chain http://localhost:8081 -intent intent_xxx`)
}
