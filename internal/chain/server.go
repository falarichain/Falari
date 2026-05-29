package chain

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"chain/internal/consensus"
	"chain/internal/wire"
)

type Server struct {
	store   *Store
	network *PeerNetwork
}

func NewServer(store *Store, network *PeerNetwork) *Server {
	return &Server{store: store, network: network}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("GET /snapshot", s.snapshot)
	mux.HandleFunc("GET /consensus", s.consensus)
	mux.HandleFunc("GET /consensus/votes", s.listConsensusVotes)
	mux.HandleFunc("POST /upgrade", s.requireOperator(s.setUpgrade))
	mux.HandleFunc("GET /upgrade", s.getUpgrade)
	mux.HandleFunc("GET /admin/mining-params", s.getMiningParams)
	mux.HandleFunc("GET /intents/{id}/health", s.intentHealth)
	mux.HandleFunc("GET /intents/health", s.allDealHealth)
	mux.HandleFunc("POST /storage/quote", s.storageQuote)
	mux.HandleFunc("GET /storage/providers", s.storageProviders)
	mux.HandleFunc("GET /storage/routes", s.storageRoutes)
	mux.HandleFunc("POST /storage/providers", s.acceptStorageProvider)
	mux.HandleFunc("POST /intents", s.createIntent)
	mux.HandleFunc("POST /intents/settle", s.settleIntent)
	mux.HandleFunc("POST /intents/permanent-fund", s.topUpPermanentFund)
	mux.HandleFunc("POST /intents/{id}/renew", s.renewDeal)
	mux.HandleFunc("POST /intents/terminate", s.terminateDeal)
	mux.HandleFunc("POST /intents/access", s.setAccessPolicy)
	mux.HandleFunc("POST /governance/proposals", s.createGovernanceProposal)
	mux.HandleFunc("POST /governance/votes", s.castGovernanceVote)
	mux.HandleFunc("POST /governance/execute", s.executeGovernanceProposal)
	mux.HandleFunc("POST /governance/cancel", s.cancelGovernanceProposal)
	mux.HandleFunc("GET /governance/proposals", s.listGovernanceProposals)
	mux.HandleFunc("GET /governance/operators", s.listGovernanceOperators)
	mux.HandleFunc("GET /intents/delete-tasks", s.listDeleteTasks)
	mux.HandleFunc("GET /intents/governance/audit", s.listGovernanceAudit)
	mux.HandleFunc("GET /governance/blacklist", s.getBlacklist)
	mux.HandleFunc("POST /governance/direct-action", s.directGovernanceAction)
	mux.HandleFunc("POST /governance/direct-action/review", s.castDirectActionReviewVote)
	mux.HandleFunc("POST /governance/direct-action/ratify", s.ratifyDirectAction)
	mux.HandleFunc("GET /governance/direct-actions", s.listDirectActions)
	mux.HandleFunc("GET /manifests/", s.getManifest)
	mux.HandleFunc("GET /user-collections", s.listUserCollections)
	mux.HandleFunc("POST /collections", s.createCollection)
	mux.HandleFunc("GET /collections/", s.getCollection)
	mux.HandleFunc("GET /collections/{id}/records", s.getCollectionRecords)
	mux.HandleFunc("POST /records", s.appendRecord)
	mux.HandleFunc("GET /records/{id}/manifest", s.getRecordManifest)
	mux.HandleFunc("GET /records/", s.getRecord)
	mux.HandleFunc("POST /key-envelopes", s.createKeyEnvelope)
	mux.HandleFunc("GET /key-envelopes", s.listKeyEnvelopes)
	mux.HandleFunc("POST /shares/address", s.createAddressShare)
	mux.HandleFunc("POST /shares/passcode", s.createPasscodeShare)
	mux.HandleFunc("POST /shares/revoke", s.revokeShare)
	mux.HandleFunc("GET /shares", s.listShares)
	mux.HandleFunc("POST /batch-commits", s.batchCommit)
	mux.HandleFunc("POST /finalize", s.finalize)
	mux.HandleFunc("POST /challenges", s.generateChallenges)
	mux.HandleFunc("GET /challenges", s.listChallenges)
	mux.HandleFunc("GET /repairs", s.repairPlan)
	mux.HandleFunc("POST /repairs", s.createRepairTasks)
	mux.HandleFunc("POST /proofs", s.submitProof)
	mux.HandleFunc("POST /delete-receipts", s.submitDeleteReceipt)
	mux.HandleFunc("POST /retrieval-receipts", s.submitRetrievalReceipt)
	mux.HandleFunc("GET /retrieval-receipts", s.listRetrievalReceipts)
	mux.HandleFunc("POST /epochs", s.startEpoch)
	mux.HandleFunc("POST /epochs/finalize", s.finalizeEpoch)
	mux.HandleFunc("GET /epochs/{id}/rewards", s.epochRewards)
	mux.HandleFunc("POST /miners", s.registerMiner)
	mux.HandleFunc("POST /miners/deregister", s.deregisterMiner)
	mux.HandleFunc("POST /miners/claim-rewards", s.claimMiningRewards)
	mux.HandleFunc("GET /miners/", s.getMinerStats)
	mux.HandleFunc("POST /validators", s.registerValidator)
	mux.HandleFunc("GET /validators", s.listValidators)
	mux.HandleFunc("GET /validators/delegations", s.listDelegationsByDelegator)
	mux.HandleFunc("POST /validators/deregister", s.deregisterValidator)
	mux.HandleFunc("POST /validators/delegate", s.delegateStake)
	mux.HandleFunc("POST /validators/undelegate", s.undelegateStake)
	mux.HandleFunc("POST /validators/rotate-operator", s.rotateOperator)
	mux.HandleFunc("GET /validators/unbonding", s.listUnbonding)
	mux.HandleFunc("POST /validators/evidence", s.submitValidatorEvidence)
	mux.HandleFunc("POST /transfer", s.transfer)
	mux.HandleFunc("GET /accounts/", s.getAccount)
	mux.HandleFunc("GET /intents/", s.getIntent)
	mux.HandleFunc("GET /mempool", s.getMempool)
	mux.HandleFunc("POST /blocks/produce", s.requireOperator(s.produceBlock))
	mux.HandleFunc("POST /blocks/votes", s.acceptBlockVote)
	mux.HandleFunc("POST /consensus/votes", s.submitConsensusVote)
	mux.HandleFunc("GET /blocks/latest", s.latestBlock)
	mux.HandleFunc("GET /blocks/", s.getBlock)
	mux.HandleFunc("POST /p2p/blocks", s.receivePeerBlock)
	mux.HandleFunc("POST /p2p/txs", s.receivePeerTransaction)
	mux.HandleFunc("GET /peers", s.listPeers)
	mux.HandleFunc("POST /agent-keys", s.registerAgentKey)
	mux.HandleFunc("GET /agent-keys", s.listAgentKeys)
	mux.HandleFunc("POST /agent-keys/revoke", s.revokeAgentKey)
	mux.HandleFunc("POST /agent-keys/extend", s.extendAgentKey)
	mux.HandleFunc("POST /agent-keys/topup", s.topupAgentKey)
	mux.HandleFunc("POST /multisig", s.createMultisig)
	mux.HandleFunc("GET /multisig", s.listMultisigWallets)
	mux.HandleFunc("GET /multisig/{address}", s.getMultisigWallet)
	mux.HandleFunc("POST /multisig/exec", s.multisigExec)
	// Bridge routes
	mux.HandleFunc("GET /bridge/config", s.getBridgeConfig)
	mux.HandleFunc("GET /bridge/outbound/{nonce}", s.getBridgeOutbound)
	mux.HandleFunc("GET /bridge/inbound/{hash}", s.getBridgeInbound)
	mux.HandleFunc("GET /bridge/pending", s.getBridgePending)
	mux.HandleFunc("POST /bridge/out", s.bridgeOut)
	mux.HandleFunc("POST /bridge/claim", s.bridgeClaim)
	mux.HandleFunc("POST /bridge/admin/config", s.requireOperator(s.bridgeAdminConfig))
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) snapshot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snapshot())
}

func (s *Server) consensus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.consensusLocked())
}

func (s *Server) setUpgrade(w http.ResponseWriter, r *http.Request) {
	var plan consensus.UpgradePlan
	if !decodeJSON(w, r, &plan) {
		return
	}
	if err := s.store.SetUpgradePlan(plan); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) getUpgrade(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.UpgradePlan())
}

func (s *Server) intentHealth(w http.ResponseWriter, r *http.Request) {
	intentID := r.PathValue("id")
	health, err := s.store.DealHealth(intentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func (s *Server) allDealHealth(w http.ResponseWriter, _ *http.Request) {
	healths, err := s.store.CheckDealHealthAll()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.DealHealthResponse{Healths: healths})
}

func (s *Server) topUpPermanentFund(w http.ResponseWriter, r *http.Request) {
	var req wire.PermanentFundTopUpRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.TopUpPermanentFund(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	resp := s.store.Status()
	if s.network != nil {
		resp.Peers = s.network.Peers()
		resp.PeerCount = len(resp.Peers)
		resp.LibP2PEnabled = s.network.LibP2PEnabled()
		resp.LibP2PID = s.network.LibP2PID()
		resp.LibP2PAddrs = s.network.LibP2PAddrs()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) storageQuote(w http.ResponseWriter, r *http.Request) {
	var req wire.StorageQuoteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.StorageQuote(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) storageProviders(w http.ResponseWriter, r *http.Request) {
	resp, err := s.store.StorageProviders(r.URL.Query().Get("shard_hash"), r.URL.Query().Get("shard_cid"), r.URL.Query().Get("intent"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) storageRoutes(w http.ResponseWriter, r *http.Request) {
	resp, err := s.store.StorageRoutes(r.URL.Query().Get("shard_hash"), r.URL.Query().Get("shard_cid"), r.URL.Query().Get("intent"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) acceptStorageProvider(w http.ResponseWriter, r *http.Request) {
	var req wire.StorageProviderAnnouncement
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.AcceptStorageProviderAnnouncement(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if s.network != nil {
		s.network.BroadcastStorageProvider(req)
	}
	writeJSON(w, http.StatusAccepted, req)
}

func (s *Server) createIntent(w http.ResponseWriter, r *http.Request) {
	var req wire.CreateIntentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CreateIntent(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) batchCommit(w http.ResponseWriter, r *http.Request) {
	var req wire.BatchCommitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.BatchCommit(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) finalize(w http.ResponseWriter, r *http.Request) {
	var req wire.FinalizeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.Finalize(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) settleIntent(w http.ResponseWriter, r *http.Request) {
	var req wire.SettleIntentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.SettleIntent(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) renewDeal(w http.ResponseWriter, r *http.Request) {
	var req wire.RenewDealRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.IntentID == "" {
		req.IntentID = r.PathValue("id")
	}
	resp, err := s.store.RenewDeal(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) terminateDeal(w http.ResponseWriter, r *http.Request) {
	var req wire.TerminateDealRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.TerminateDeal(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) setAccessPolicy(w http.ResponseWriter, r *http.Request) {
	var req wire.SetAccessPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.SetAccessPolicy(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) createGovernanceProposal(w http.ResponseWriter, r *http.Request) {
	var req wire.CreateGovernanceProposalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CreateGovernanceProposal(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) castGovernanceVote(w http.ResponseWriter, r *http.Request) {
	var req wire.CastGovernanceVoteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CastGovernanceVote(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) executeGovernanceProposal(w http.ResponseWriter, r *http.Request) {
	var req wire.ExecuteGovernanceProposalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.ExecuteGovernanceProposal(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) cancelGovernanceProposal(w http.ResponseWriter, r *http.Request) {
	var req wire.CreateGovernanceProposalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CancelGovernanceProposal(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listGovernanceProposals(w http.ResponseWriter, r *http.Request) {
	resp := s.store.GovernanceProposals(r.URL.Query().Get("status"), r.URL.Query().Get("intent"))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listGovernanceOperators(w http.ResponseWriter, r *http.Request) {
	resp := s.store.GovernanceOperators()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getBlacklist(w http.ResponseWriter, r *http.Request) {
	resp := s.store.Blacklist()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listDeleteTasks(w http.ResponseWriter, r *http.Request) {
	resp := s.store.DeleteTasks(r.URL.Query().Get("intent"), r.URL.Query().Get("miner"), r.URL.Query().Get("status"))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listGovernanceAudit(w http.ResponseWriter, r *http.Request) {
	resp := s.store.GovernanceAudit(r.URL.Query().Get("intent"), r.URL.Query().Get("operator"), r.URL.Query().Get("action"))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) directGovernanceAction(w http.ResponseWriter, r *http.Request) {
	var req wire.DirectGovernanceActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.DirectGovernanceAction(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) castDirectActionReviewVote(w http.ResponseWriter, r *http.Request) {
	var req wire.DirectActionReviewVoteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CastDirectActionReviewVote(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) ratifyDirectAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ActionID string `json:"action_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ActionID == "" {
		writeError(w, http.StatusBadRequest, errors.New("action_id is required"))
		return
	}
	if err := s.store.RatifyDirectAction(req.ActionID, time.Now().Unix()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ratified"})
}

func (s *Server) listDirectActions(w http.ResponseWriter, r *http.Request) {
	resp := s.store.ListDirectActions(r.URL.Query().Get("intent"), r.URL.Query().Get("status"))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) generateChallenges(w http.ResponseWriter, r *http.Request) {
	var req wire.GenerateChallengeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.GenerateChallenges(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) listChallenges(w http.ResponseWriter, r *http.Request) {
	miner := r.URL.Query().Get("miner")
	pendingOnly := r.URL.Query().Get("pending") == "true"
	resp, err := s.store.ListChallenges(miner, pendingOnly)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) repairPlan(w http.ResponseWriter, r *http.Request) {
	intentID := r.URL.Query().Get("intent")
	miner := r.URL.Query().Get("miner")
	if intentID == "" && miner != "" {
		resp, err := s.store.PendingRepairTasks(miner)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp, err := s.store.RepairPlan(intentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) createRepairTasks(w http.ResponseWriter, r *http.Request) {
	var req wire.CreateRepairRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CreateRepairTasks(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) submitProof(w http.ResponseWriter, r *http.Request) {
	var req wire.SubmitProofRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.SubmitProof(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) submitDeleteReceipt(w http.ResponseWriter, r *http.Request) {
	var req wire.SubmitDeleteReceiptRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.SubmitDeleteReceipt(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) submitRetrievalReceipt(w http.ResponseWriter, r *http.Request) {
	var req wire.SubmitRetrievalReceiptRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.SubmitRetrievalReceipt(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listRetrievalReceipts(w http.ResponseWriter, r *http.Request) {
	resp := s.store.RetrievalReceipts(r.URL.Query().Get("intent"), r.URL.Query().Get("miner"))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) startEpoch(w http.ResponseWriter, r *http.Request) {
	if _, err := s.validateOperatorHeaders(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var req wire.StartEpochRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.StartEpoch(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) finalizeEpoch(w http.ResponseWriter, r *http.Request) {
	if _, err := s.validateOperatorHeaders(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var req wire.FinalizeEpochRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.FinalizeEpoch(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) epochRewards(w http.ResponseWriter, r *http.Request) {
	epochID := r.PathValue("id")
	resp, err := s.store.EpochRewards(epochID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getMinerStats(w http.ResponseWriter, r *http.Request) {
	minerAddress := strings.TrimPrefix(r.URL.Path, "/miners/")
	resp, err := s.store.MinerStats(minerAddress)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) registerMiner(w http.ResponseWriter, r *http.Request) {
	var req wire.RegisterMinerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.RegisterMiner(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) deregisterMiner(w http.ResponseWriter, r *http.Request) {
	var req wire.DeregisterMinerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.DeregisterMiner(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": wire.MinerStatusExiting})
}

func (s *Server) claimMiningRewards(w http.ResponseWriter, r *http.Request) {
	var req wire.ClaimMiningRewardsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.ClaimMiningRewards(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) registerValidator(w http.ResponseWriter, r *http.Request) {
	var req wire.RegisterValidatorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.RegisterValidator(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) deregisterValidator(w http.ResponseWriter, r *http.Request) {
	var req wire.DeregisterValidatorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.DeregisterValidator(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": wire.ValidatorStatusExiting})
}

func (s *Server) listValidators(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Validators())
}

func (s *Server) listDelegationsByDelegator(w http.ResponseWriter, r *http.Request) {
	delegator := r.URL.Query().Get("delegator")
	if delegator == "" {
		writeError(w, http.StatusBadRequest, errors.New("delegator query parameter is required"))
		return
	}
	delegations := s.store.DelegationsByDelegator(delegator)
	if delegations == nil {
		delegations = []wire.StakeDelegation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"delegations": delegations})
}

func (s *Server) delegateStake(w http.ResponseWriter, r *http.Request) {
	var req wire.DelegateStakeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.DelegateStake(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) undelegateStake(w http.ResponseWriter, r *http.Request) {
	var req wire.UndelegateStakeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.UndelegateStake(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) rotateOperator(w http.ResponseWriter, r *http.Request) {
	var req wire.RotateOperatorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.RotateOperator(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listUnbonding(w http.ResponseWriter, r *http.Request) {
	delegator := r.URL.Query().Get("delegator")
	if delegator == "" {
		writeError(w, http.StatusBadRequest, errors.New("delegator query parameter is required"))
		return
	}
	entries := s.store.ListUnbonding(delegator)
	if entries == nil {
		entries = []wire.UnbondingEntry{}
	}
	writeJSON(w, http.StatusOK, wire.ListUnbondingResponse{Entries: entries})
}

func (s *Server) submitValidatorEvidence(w http.ResponseWriter, r *http.Request) {
	var req wire.SubmitValidatorEvidenceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.SubmitValidatorEvidence(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) transfer(w http.ResponseWriter, r *http.Request) {
	var req wire.TransferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.Transfer(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimPrefix(r.URL.Path, "/accounts/")
	resp, err := s.store.Account(address)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getIntent(w http.ResponseWriter, r *http.Request) {
	intentID := strings.TrimPrefix(r.URL.Path, "/intents/")
	resp, err := s.store.GetIntent(intentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getManifest(w http.ResponseWriter, r *http.Request) {
	intentID := strings.TrimPrefix(r.URL.Path, "/manifests/")
	resp, err := s.store.Manifest(intentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listUserCollections(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		writeError(w, http.StatusBadRequest, errors.New("user query parameter is required"))
		return
	}
	resp, err := s.store.ListCollectionsByUser(user)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	var req wire.CreateCollectionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CreateCollection(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) appendRecord(w http.ResponseWriter, r *http.Request) {
	var req wire.AppendRecordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.AppendRecord(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) getRecord(w http.ResponseWriter, r *http.Request) {
	recordID := strings.TrimPrefix(r.URL.Path, "/records/")
	resp, err := s.store.DataRecord(recordID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getRecordManifest(w http.ResponseWriter, r *http.Request) {
	recordID := r.PathValue("id")
	resp, err := s.store.RecordManifest(recordID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	collectionID := strings.TrimPrefix(r.URL.Path, "/collections/")
	collectionID = strings.TrimSuffix(collectionID, "/records")
	if strings.HasSuffix(r.URL.Path, "/records") {
		resp, err := s.store.CollectionRecordsFiltered(collectionID, collectionRecordFilterFromRequest(r))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp, err := s.store.Collection(collectionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getCollectionRecords(w http.ResponseWriter, r *http.Request) {
	collectionID := r.PathValue("id")
	resp, err := s.store.CollectionRecordsFiltered(collectionID, collectionRecordFilterFromRequest(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func collectionRecordFilterFromRequest(r *http.Request) wire.CollectionRecordFilter {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	after, _ := strconv.ParseInt(query.Get("after"), 10, 64)
	before, _ := strconv.ParseInt(query.Get("before"), 10, 64)
	return wire.CollectionRecordFilter{
		Kind:         query.Get("kind"),
		Key:          query.Get("key"),
		ParentRecord: query.Get("parent"),
		AfterUnix:    after,
		BeforeUnix:   before,
		Limit:        limit,
		Reverse:      query.Get("reverse") == "true" || query.Get("latest") == "true",
	}
}

func (s *Server) getMempool(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Mempool())
}

func (s *Server) produceBlock(w http.ResponseWriter, _ *http.Request) {
	resp, err := s.store.ProduceBlock()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) acceptBlockVote(w http.ResponseWriter, r *http.Request) {
	var vote wire.BlockVote
	if !decodeJSON(w, r, &vote) {
		return
	}
	resp, err := s.store.AcceptBlockVote(vote)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) submitConsensusVote(w http.ResponseWriter, r *http.Request) {
	var req wire.SubmitConsensusVoteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.SubmitConsensusVote(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listConsensusVotes(w http.ResponseWriter, r *http.Request) {
	height, _ := strconv.ParseUint(r.URL.Query().Get("height"), 10, 64)
	round, _ := strconv.ParseUint(r.URL.Query().Get("round"), 10, 64)
	resp := s.store.ConsensusVotes(height, round, r.URL.Query().Get("type"))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) latestBlock(w http.ResponseWriter, _ *http.Request) {
	block, err := s.store.LatestBlock()
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.BlockResponse{Block: block})
}

func (s *Server) getBlock(w http.ResponseWriter, r *http.Request) {
	rawHeight := strings.TrimPrefix(r.URL.Path, "/blocks/")
	height, err := strconv.ParseUint(rawHeight, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	block, err := s.store.GetBlock(height)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.BlockResponse{Block: block})
}

func (s *Server) receivePeerBlock(w http.ResponseWriter, r *http.Request) {
	var block wire.Block
	if !decodeJSON(w, r, &block) {
		return
	}
	accepted, err := s.store.AcceptBlock(block)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if accepted {
		s.store.SubmitLocalConsensusVotesForBlock(block)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": accepted,
		"height":   block.Height,
		"hash":     block.Hash,
	})
}

func (s *Server) receivePeerTransaction(w http.ResponseWriter, r *http.Request) {
	var tx wire.Transaction
	if !decodeJSON(w, r, &tx) {
		return
	}
	accepted, err := s.store.AcceptTransaction(tx)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if accepted && s.network != nil {
		s.network.BroadcastTransaction(tx)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": accepted,
		"tx_id":    tx.TxID,
	})
}

func (s *Server) listPeers(w http.ResponseWriter, _ *http.Request) {
	if s.network == nil {
		writeJSON(w, http.StatusOK, map[string]any{"peers": []string{}, "libp2p_addrs": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"peers":        s.network.Peers(),
		"libp2p_id":    s.network.LibP2PID(),
		"libp2p_addrs": s.network.LibP2PAddrs(),
	})
}

func (s *Server) createKeyEnvelope(w http.ResponseWriter, r *http.Request) {
	var req wire.CreateKeyEnvelopeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CreateKeyEnvelope(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) listKeyEnvelopes(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	envelopes, err := s.store.ListKeyEnvelopes(
		query.Get("intent_id"),
		query.Get("recipient"),
		query.Get("recipient_type"),
		query.Get("share_id"),
		query.Get("include_revoked") == "true",
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"envelopes": envelopes})
}

func (s *Server) createAddressShare(w http.ResponseWriter, r *http.Request) {
	var req wire.CreateAddressShareRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CreateAddressShare(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) createPasscodeShare(w http.ResponseWriter, r *http.Request) {
	var req wire.CreatePasscodeShareRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.CreatePasscodeShare(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	var req wire.RevokeShareRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.RevokeShare(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked", "share_id": req.ShareID})
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	resp, err := s.store.ListShares(
		query.Get("intent_id"),
		query.Get("owner"),
		query.Get("recipient"),
		query.Get("share_id"),
		query.Get("include_revoked") == "true",
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getMiningParams(w http.ResponseWriter, _ *http.Request) {
	params := s.store.GetMiningParams()
	writeJSON(w, http.StatusOK, params)
}

const maxRequestSize = 1 << 20 // 1 MB

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestSize)
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) registerAgentKey(w http.ResponseWriter, r *http.Request) {
	var req wire.RegisterAgentKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.RegisterAgentKey(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) listAgentKeys(w http.ResponseWriter, r *http.Request) {
	master := r.URL.Query().Get("master")
	if master == "" {
		writeError(w, http.StatusBadRequest, errors.New("master query parameter is required"))
		return
	}
	keys, err := s.store.ListAgentKeys(master)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.ListAgentKeysResponse{Keys: keys})
}

func (s *Server) revokeAgentKey(w http.ResponseWriter, r *http.Request) {
	var req wire.RevokeAgentKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.RevokeAgentKey(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked", "key_id": req.KeyID})
}

func (s *Server) extendAgentKey(w http.ResponseWriter, r *http.Request) {
	var req wire.ExtendAgentKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.ExtendAgentKey(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) topupAgentKey(w http.ResponseWriter, r *http.Request) {
	var req wire.TopupAgentKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.TopupAgentKey(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── Multisig handlers ──

func (s *Server) createMultisig(w http.ResponseWriter, r *http.Request) {
	var req wire.MultisigCreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	wallet, err := s.store.CreateMultisigWallet(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, wallet)
}

func (s *Server) getMultisigWallet(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")
	if address == "" {
		writeError(w, http.StatusBadRequest, errors.New("address is required"))
		return
	}
	info, err := s.store.GetMultisigWallet(address)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) multisigExec(w http.ResponseWriter, r *http.Request) {
	var req wire.MultisigExecRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.store.MultisigExec(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listMultisigWallets(w http.ResponseWriter, r *http.Request) {
	signer := r.URL.Query().Get("signer")
	wallets, err := s.store.ListMultisigWallets(signer)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.MultisigWalletListResponse{Wallets: wallets})
}
