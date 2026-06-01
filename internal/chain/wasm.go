package chain

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"chain/internal/wasm"
	"chain/internal/wire"
)

// ── Transaction payload wrappers (for recordTxLocked + block replay) ──

type deployContractTxPayload struct {
	Request         wire.DeployContractRequest  `json:"request"`
	Response        wire.DeployContractResponse `json:"response"`
	BytecodeHash    string                     `json:"bytecode_hash"`
	ContractAddress string                     `json:"contract_address"`
	DeployedAtUnix  int64                      `json:"deployed_at_unix"`
	StateDelta      *WasmStateDelta            `json:"state_delta,omitempty"`
}

type callContractTxPayload struct {
	Request    wire.CallContractRequest  `json:"request"`
	Response   wire.CallContractResponse `json:"response"`
	StateDelta *WasmStateDelta           `json:"state_delta,omitempty"`
}

type destroyContractTxPayload struct {
	Request  wire.DestroyContractRequest  `json:"request"`
	Response wire.DestroyContractResponse `json:"response"`
}

type wasmCronExecTxPayload struct {
	Payload    wire.WasmCronExecPayload `json:"payload"`
	StateDelta *WasmStateDelta          `json:"state_delta,omitempty"`
}

type wasmEventDeliveryTxPayload struct {
	Payload    wire.WasmEventDeliveryPayload `json:"payload"`
	StateDelta *WasmStateDelta               `json:"state_delta,omitempty"`
}

// ── WASM Engine management ──

// getWasmEngine returns the WASM engine, lazily initializing it if needed.
// On first creation, it recompiles all persisted WASM bytecode into the
// compiled module cache so that contracts are callable after node restart.
func (s *Store) getWasmEngine() (*wasm.WasmEngine, error) {
	if s.wasmEngine != nil {
		if engine, ok := s.wasmEngine.(*wasm.WasmEngine); ok {
			return engine, nil
		}
	}
	// Lazily create a new engine with the host API registrar.
	engine, err := wasm.NewWasmEngine(s.registerHostFunctions)
	if err != nil {
		return nil, fmt.Errorf("failed to create WASM engine: %w", err)
	}
	s.wasmEngine = engine

	// Recompile all active contract modules from persisted bytecode.
	// This restores the in-memory compiled cache after a node restart.
	modules := make(map[string][]byte)
	for _, code := range s.data.WasmCodes {
		if code.BytecodeBase64 == "" {
			continue
		}
		bytecode, decErr := base64.StdEncoding.DecodeString(code.BytecodeBase64)
		if decErr != nil {
			continue
		}
		modules[code.Hash] = bytecode
	}
	if len(modules) > 0 {
		_, errs := engine.RecompileAll(context.Background(), modules)
		for _, e := range errs {
			fmt.Printf("[wasm] warning: %v\n", e)
		}
	}

	return engine, nil
}

// ── Contract address generation ──

// generateContractAddress creates a deterministic contract address from the
// deployer address and the global WASM nonce.
func generateContractAddress(deployer string, nonce uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, nonce)
	hash := sha256.Sum256(append([]byte(strings.ToLower(deployer)), buf...))
	return "0x" + hex.EncodeToString(hash[:20])
}

// ── DeployContract ──

// DeployContract deploys a new WASM smart contract.
func (s *Store) DeployContract(req wire.DeployContractRequest) (wire.DeployContractResponse, error) {
	// Validate fields.
	if req.Deployer == "" {
		return wire.DeployContractResponse{}, errors.New("deployer is required")
	}
	if req.BytecodeBase64 == "" {
		return wire.DeployContractResponse{}, errors.New("bytecode_base64 is required")
	}
	if len(req.Label) > wire.MaxWasmLabelLen {
		return wire.DeployContractResponse{}, fmt.Errorf("label exceeds max length %d", wire.MaxWasmLabelLen)
	}
	if req.InitMethod == "" {
		req.InitMethod = "init"
	}
	if len(req.InitMethod) > wire.MaxWasmMethodNameLen {
		return wire.DeployContractResponse{}, fmt.Errorf("init_method exceeds max length %d", wire.MaxWasmMethodNameLen)
	}

	// Decode bytecode.
	bytecode, err := base64.StdEncoding.DecodeString(req.BytecodeBase64)
	if err != nil {
		return wire.DeployContractResponse{}, fmt.Errorf("invalid bytecode_base64: %w", err)
	}
	bytecodeHash := wasm.ComputeCodeHash(bytecode)

	// Lock mutex.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify chain_id.
	if req.ChainID != s.data.ChainID {
		return wire.DeployContractResponse{}, errors.New("chain_id mismatch")
	}

	// Verify signature.
	deployer := wire.NormalizeAddress(req.Deployer)
	if requestUsesAgent(req.AgentKeyID) {
		spend := req.Fee + req.InitFund
		if spend < req.Fee {
			return wire.DeployContractResponse{}, errors.New("fee + init_fund overflows")
		}
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, deployer, "deploy_contract", spend, func(agentPub string) error {
			return wire.VerifyDeployContractAgent(req, s.data.ChainID, bytecodeHash, agentPub)
		}); err != nil {
			return wire.DeployContractResponse{}, err
		}
	} else {
		if err := wire.VerifyDeployContractSignature(req, s.data.ChainID, bytecodeHash); err != nil {
			return wire.DeployContractResponse{}, fmt.Errorf("signature verification failed: %w", err)
		}
	}

	// Verify nonce.
	account := s.accountLocked(deployer)
	if req.Nonce != account.Nonce {
		return wire.DeployContractResponse{}, errors.New("invalid nonce")
	}

	// Verify fee.
	if err := s.validateWasmFeeLocked(req.Fee, "deploy_contract"); err != nil {
		return wire.DeployContractResponse{}, err
	}

	// Get WASM engine.
	engine, err := s.getWasmEngine()
	if err != nil {
		return wire.DeployContractResponse{}, err
	}

	// Compile module.
	ctx := context.Background()
	codeHash, err := engine.CompileModule(ctx, bytecode)
	if err != nil {
		return wire.DeployContractResponse{}, fmt.Errorf("WASM compilation failed: %w", err)
	}

	// Generate contract address.
	contractAddr := generateContractAddress(deployer, s.data.WasmNonce)

	// Validate balance for fee + init_fund before consuming agent request.
	if account.Balance < req.Fee {
		return wire.DeployContractResponse{}, errors.New("insufficient balance for fee")
	}
	if req.InitFund > 0 && account.Balance-req.Fee < req.InitFund {
		return wire.DeployContractResponse{}, errors.New("insufficient balance for init_fund")
	}

	// Consume agent request (increment nonce + usage counters).
	if requestUsesAgent(req.AgentKeyID) {
		spend := req.Fee + req.InitFund
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, spend); err != nil {
			return wire.DeployContractResponse{}, err
		}
	}

	// Commit state mutations.
	s.data.WasmNonce++
	account.Balance -= req.Fee
	account.Nonce++
	s.data.Accounts[account.Address] = account

	// Transfer InitFund to contract.
	if req.InitFund > 0 {
		account.Balance -= req.InitFund
		s.data.Accounts[account.Address] = account
		contractAccount := s.accountLocked(contractAddr)
		contractAccount.Balance += req.InitFund
		s.data.Accounts[contractAccount.Address] = contractAccount
	}

	// Store or increment code.
	now := time.Now().Unix()
	if existing, ok := s.data.WasmCodes[codeHash]; ok {
		existing.RefCount++
	} else {
		s.data.WasmCodes[codeHash] = &wire.WasmCode{
			Hash:           codeHash,
			BytecodeBase64: req.BytecodeBase64,
			SizeBytes:      int64(len(bytecode)),
			RefCount:       1,
			UploadedAtUnix: now,
		}
	}

	// Create contract record.
	contract := &wire.WasmContract{
		Address:       contractAddr,
		CodeHash:      codeHash,
		Admin:         deployer,
		Label:         req.Label,
		Balance:       req.InitFund,
		Status:        wire.WasmContractStatusActive,
		PublicKV:      req.PublicKV,
		CreatedAtUnix: now,
		UpdatedAtUnix: now,
	}
	s.data.WasmContracts[contractAddr] = contract

	// Initialize KV store.
	s.data.WasmKVStore[contractAddr] = map[string]string{}

	// Execute init method (if bytecode exports it).
	var initResult string
	var gasUsed uint64
	var initDelta *WasmStateDelta
	if engine.HasModule(codeHash) {
		initArgs := []byte("{}")
		if req.InitArgs != "" {
			initArgs = []byte(req.InitArgs)
		}
		// Build context with contract identity and block time.
		execCtx := withContractAddress(ctx, contractAddr)
		execCtx = withBlockTime(execCtx, now)
		execCtx = withEventCounter(execCtx, wire.MaxWasmEventsPerCall)

		// Snapshot state before init execution.
		before := captureWasmStateSnapshot(s, contractAddr)

		result, execErr := engine.CallExport(execCtx, codeHash, req.InitMethod, initArgs, wire.DefaultWasmGasLimit)

		// Diff state after init execution.
		delta := diffWasmState(s, before, contractAddr)
		initDelta = &delta

		if execErr == nil && result != nil {
			gasUsed = result.GasUsed
			initResult = string(result.Data)
		}
		// init failure is not fatal — contract is still deployed
	}

	// Register cron jobs.
	for _, spec := range req.CronJobs {
		s.registerWasmCronLocked(contractAddr, spec, now)
	}

	resp := wire.DeployContractResponse{
		Contract:   *contract,
		InitResult: initResult,
		GasUsed:    gasUsed,
	}

	// Record transaction.
	s.recordTxLocked("deploy_contract", deployer, deployContractTxPayload{
		Request:         req,
		Response:        resp,
		BytecodeHash:    bytecodeHash,
		ContractAddress: contractAddr,
		DeployedAtUnix:  now,
		StateDelta:      initDelta,
	})

	// Emit event.
	s.emitEventWithEmitterLocked(wire.EventContractDeployed, map[string]any{
		"contract_address": contractAddr,
		"deployer":         deployer,
		"label":            req.Label,
		"code_hash":        codeHash,
	}, deployer, "", "", int64(len(s.data.Blocks)), deployer)

	if err := s.saveLocked(); err != nil {
		return wire.DeployContractResponse{}, err
	}
	return resp, nil
}

// ── CallContract ──

// CallContract invokes a method on a deployed WASM contract.
func (s *Store) CallContract(req wire.CallContractRequest) (wire.CallContractResponse, error) {
	if req.Caller == "" {
		return wire.CallContractResponse{}, errors.New("caller is required")
	}
	if req.ContractAddress == "" {
		return wire.CallContractResponse{}, errors.New("contract_address is required")
	}
	if req.Method == "" {
		return wire.CallContractResponse{}, errors.New("method is required")
	}
	if len(req.Method) > wire.MaxWasmMethodNameLen {
		return wire.CallContractResponse{}, fmt.Errorf("method exceeds max length %d", wire.MaxWasmMethodNameLen)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify chain_id.
	if req.ChainID != s.data.ChainID {
		return wire.CallContractResponse{}, errors.New("chain_id mismatch")
	}

	// Verify signature.
	caller := wire.NormalizeAddress(req.Caller)
	if requestUsesAgent(req.AgentKeyID) {
		spend := req.Fee + req.Fund
		if spend < req.Fee {
			return wire.CallContractResponse{}, errors.New("fee + fund overflows")
		}
		if err := s.verifyAgentRequestLocked(req.ChainID, req.AgentKeyID, req.AgentNonce, caller, "call_contract", spend, func(agentPub string) error {
			return wire.VerifyCallContractAgent(req, agentPub)
		}); err != nil {
			return wire.CallContractResponse{}, err
		}
	} else {
		if err := wire.VerifyCallContractSignature(req); err != nil {
			return wire.CallContractResponse{}, fmt.Errorf("signature verification failed: %w", err)
		}
	}

	// Verify nonce.
	account := s.accountLocked(caller)
	if req.Nonce != account.Nonce {
		return wire.CallContractResponse{}, errors.New("invalid nonce")
	}

	// Verify fee.
	if err := s.validateWasmFeeLocked(req.Fee, "call_contract"); err != nil {
		return wire.CallContractResponse{}, err
	}

	// Look up contract.
	contractAddr := wire.NormalizeAddress(req.ContractAddress)
	contract, ok := s.data.WasmContracts[contractAddr]
	if !ok {
		return wire.CallContractResponse{}, errors.New("contract not found")
	}
	if contract.Status != wire.WasmContractStatusActive {
		return wire.CallContractResponse{}, errors.New("contract is not active")
	}

	// Validate balance for fee + fund before consuming agent request.
	if account.Balance < req.Fee {
		return wire.CallContractResponse{}, errors.New("insufficient balance for fee")
	}
	if req.Fund > 0 && account.Balance-req.Fee < req.Fund {
		return wire.CallContractResponse{}, errors.New("insufficient balance for fund")
	}

	// Consume agent request (increment nonce + usage counters).
	if requestUsesAgent(req.AgentKeyID) {
		spend := req.Fee + req.Fund
		if err := s.consumeAgentRequestLocked(req.AgentKeyID, spend); err != nil {
			return wire.CallContractResponse{}, err
		}
	}

	// Deduct fee from caller.
	account.Balance -= req.Fee
	account.Nonce++
	s.data.Accounts[account.Address] = account

	// Transfer fund to contract.
	if req.Fund > 0 {
		account.Balance -= req.Fund
		s.data.Accounts[account.Address] = account
		contract.Balance += req.Fund
		s.data.WasmContracts[contractAddr] = contract
	}

	// Get WASM engine and execute.
	engine, err := s.getWasmEngine()
	if err != nil {
		return wire.CallContractResponse{}, err
	}

	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = wire.DefaultWasmGasLimit
	}

	args := []byte("{}")
	if req.Args != "" {
		args = []byte(req.Args)
	}

	// Build context with contract identity, block time, and event counter.
	now := time.Now().Unix()
	execCtx := withContractAddress(context.Background(), contractAddr)
	execCtx = withBlockTime(execCtx, now)
	execCtx = withEventCounter(execCtx, wire.MaxWasmEventsPerCall)

	// Snapshot state before WASM execution.
	before := captureWasmStateSnapshot(s, contractAddr)

	result, execErr := engine.CallExport(execCtx, contract.CodeHash, req.Method, args, gasLimit)

	// Diff state after WASM execution.
	delta := diffWasmState(s, before, contractAddr)

	var resultData string
	var gasUsed uint64
	if result != nil {
		gasUsed = result.GasUsed
		resultData = string(result.Data)
	}

	if execErr != nil {
		// Execution failed — still record the tx and charge the fee,
		// but return the error to the caller.
		resp := wire.CallContractResponse{
			Result:  fmt.Sprintf("error: %v", execErr),
			GasUsed: gasUsed,
		}
		s.recordTxLocked("call_contract", caller, callContractTxPayload{
			Request:    req,
			Response:   resp,
			StateDelta: &delta,
		})
		if err := s.saveLocked(); err != nil {
			return wire.CallContractResponse{}, err
		}
		return resp, nil
	}

	resp := wire.CallContractResponse{
		Result:  resultData,
		GasUsed: gasUsed,
	}

	// Record transaction.
	s.recordTxLocked("call_contract", caller, callContractTxPayload{
		Request:    req,
		Response:   resp,
		StateDelta: &delta,
	})

	// Emit event.
	s.emitEventWithEmitterLocked(wire.EventContractCalled, map[string]any{
		"contract_address": contractAddr,
		"caller":           caller,
		"method":           req.Method,
		"gas_used":         gasUsed,
	}, caller, "", contractAddr, int64(len(s.data.Blocks)), caller)

	if err := s.saveLocked(); err != nil {
		return wire.CallContractResponse{}, err
	}
	return resp, nil
}

// ── DestroyContract ──

// DestroyContract destroys a contract (admin only), recovering its balance.
func (s *Store) DestroyContract(req wire.DestroyContractRequest) (wire.DestroyContractResponse, error) {
	if req.Admin == "" {
		return wire.DestroyContractResponse{}, errors.New("admin is required")
	}
	if req.ContractAddress == "" {
		return wire.DestroyContractResponse{}, errors.New("contract_address is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify chain_id.
	if req.ChainID != s.data.ChainID {
		return wire.DestroyContractResponse{}, errors.New("chain_id mismatch")
	}

	// Verify signature.
	if err := wire.VerifyDestroyContractSignature(req); err != nil {
		return wire.DestroyContractResponse{}, fmt.Errorf("signature verification failed: %w", err)
	}

	// Verify nonce.
	admin := wire.NormalizeAddress(req.Admin)
	account := s.accountLocked(admin)
	if req.Nonce != account.Nonce {
		return wire.DestroyContractResponse{}, errors.New("invalid nonce")
	}

	// Verify fee.
	if err := s.validateWasmFeeLocked(req.Fee, "destroy_contract"); err != nil {
		return wire.DestroyContractResponse{}, err
	}

	// Look up contract.
	contractAddr := wire.NormalizeAddress(req.ContractAddress)
	contract, ok := s.data.WasmContracts[contractAddr]
	if !ok {
		return wire.DestroyContractResponse{}, errors.New("contract not found")
	}
	if contract.Status != wire.WasmContractStatusActive {
		return wire.DestroyContractResponse{}, errors.New("contract is already destroyed")
	}

	// Verify admin.
	if !strings.EqualFold(contract.Admin, admin) {
		return wire.DestroyContractResponse{}, errors.New("only the contract admin can destroy it")
	}

	// Deduct fee from admin.
	if account.Balance < req.Fee {
		return wire.DestroyContractResponse{}, errors.New("insufficient balance for fee")
	}
	account.Balance -= req.Fee
	account.Nonce++
	s.data.Accounts[account.Address] = account

	// Transfer contract balance to admin.
	recoveredBalance := contract.Balance
	if recoveredBalance > 0 {
		account.Balance += recoveredBalance
		s.data.Accounts[account.Address] = account
		contract.Balance = 0
	}

	// Mark as destroyed.
	contract.Status = wire.WasmContractStatusDestroyed
	contract.UpdatedAtUnix = time.Now().Unix()
	s.data.WasmContracts[contractAddr] = contract

	// Clean up cron jobs.
	delete(s.data.WasmCronJobs, contractAddr)

	// Clean up KV store.
	delete(s.data.WasmKVStore, contractAddr)

	// Clean up event subscriptions for this contract.
	delete(s.data.WasmEventSubscriptions, contractAddr)

	// Remove subscriptions targeting this contract from other contracts.
	for subAddr, subs := range s.data.WasmEventSubscriptions {
		filtered := make([]wire.WasmEventSubscription, 0, len(subs))
		for _, sub := range subs {
			if !strings.EqualFold(sub.EmitterAddress, contractAddr) {
				filtered = append(filtered, sub)
			}
		}
		if len(filtered) != len(subs) {
			s.data.WasmEventSubscriptions[subAddr] = filtered
		}
	}

	// Remove pending events targeting this contract.
	remaining := make([]wire.WasmPendingEventDelivery, 0)
	for _, pending := range s.data.WasmPendingEvents {
		if !strings.EqualFold(pending.Event.EmitterAddress, contractAddr) {
			remaining = append(remaining, pending)
		}
	}
	s.data.WasmPendingEvents = remaining

	// Decrement code ref count.
	if code, ok := s.data.WasmCodes[contract.CodeHash]; ok {
		code.RefCount--
		if code.RefCount <= 0 {
			delete(s.data.WasmCodes, contract.CodeHash)
		}
	}

	resp := wire.DestroyContractResponse{
		RecoveredBalance: recoveredBalance,
		Status:           wire.WasmContractStatusDestroyed,
	}

	// Record transaction.
	s.recordTxLocked("destroy_contract", admin, destroyContractTxPayload{
		Request:  req,
		Response: resp,
	})

	// Emit event.
	s.emitEventWithEmitterLocked(wire.EventContractDestroyed, map[string]any{
		"contract_address":  contractAddr,
		"admin":             admin,
		"recovered_balance": recoveredBalance,
	}, admin, "", contractAddr, int64(len(s.data.Blocks)), admin)

	if err := s.saveLocked(); err != nil {
		return wire.DestroyContractResponse{}, err
	}
	return resp, nil
}

// ── Query methods ──

// GetContract returns a contract with its cron jobs and subscriptions.
func (s *Store) GetContract(address string) (wire.WasmContractInfo, error) {
	address = wire.NormalizeAddress(address)
	s.mu.Lock()
	defer s.mu.Unlock()

	contract, ok := s.data.WasmContracts[address]
	if !ok {
		return wire.WasmContractInfo{}, errors.New("contract not found")
	}
	return wire.WasmContractInfo{
		Contract:      *contract,
		CronJobs:      s.data.WasmCronJobs[address],
		Subscriptions: s.data.WasmEventSubscriptions[address],
	}, nil
}

// ListContracts returns all contracts.
func (s *Store) ListContracts() []wire.WasmContractInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]wire.WasmContractInfo, 0, len(s.data.WasmContracts))
	for addr, contract := range s.data.WasmContracts {
		result = append(result, wire.WasmContractInfo{
			Contract:      *contract,
			CronJobs:      s.data.WasmCronJobs[addr],
			Subscriptions: s.data.WasmEventSubscriptions[addr],
		})
	}
	return result
}

// GetContractKV returns the KV store entries for a contract.
func (s *Store) GetContractKV(address string) (wire.WasmKVResponse, error) {
	address = wire.NormalizeAddress(address)
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data.WasmContracts[address]; !ok {
		return wire.WasmKVResponse{}, errors.New("contract not found")
	}
	entries := s.data.WasmKVStore[address]
	if entries == nil {
		entries = map[string]string{}
	}
	return wire.WasmKVResponse{
		ContractAddress: address,
		Entries:         entries,
	}, nil
}

// GetContractCode returns the WASM code for a given hash.
func (s *Store) GetContractCode(hash string) (*wire.WasmCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	code, ok := s.data.WasmCodes[hash]
	if !ok {
		return nil, errors.New("code not found")
	}
	return code, nil
}

// ── Internal helpers ──

// validateWasmFeeLocked checks that the fee meets the minimum requirement
// for the given WASM transaction type.
func (s *Store) validateWasmFeeLocked(fee uint64, txType string) error {
	multiplierBPS := s.transactionFeeMultiplierBPS(txType)
	requiredFee := s.data.FeeMarket.BaseFee * multiplierBPS / 10000
	if fee < requiredFee {
		return fmt.Errorf("fee %d below required %d for %s", fee, requiredFee, txType)
	}
	return nil
}

// executeWasmMethodLocked is a shared helper for cron and event execution.
// It instantiates and calls a contract method within the locked state.
// Returns (resultData, gasUsed, stateDelta, error).
func (s *Store) executeWasmMethodLocked(contractAddr string, method string, input []byte, blockTime int64) (string, uint64, *WasmStateDelta, error) {
	contract, ok := s.data.WasmContracts[contractAddr]
	if !ok || contract.Status != wire.WasmContractStatusActive {
		return "", 0, nil, errors.New("contract not found or not active")
	}

	engine, err := s.getWasmEngine()
	if err != nil {
		return "", 0, nil, err
	}

	// Snapshot state before WASM execution.
	before := captureWasmStateSnapshot(s, contractAddr)

	ctx := withContractAddress(context.Background(), contractAddr)
	ctx = withBlockTime(ctx, blockTime)
	ctx = withEventCounter(ctx, wire.MaxWasmEventsPerCall)
	result, execErr := engine.CallExport(ctx, contract.CodeHash, method, input, wire.DefaultWasmGasLimit)

	// Diff state after WASM execution.
	delta := diffWasmState(s, before, contractAddr)

	var resultData string
	var gasUsed uint64
	if result != nil {
		gasUsed = result.GasUsed
		resultData = string(result.Data)
	}
	if execErr != nil {
		return resultData, gasUsed, &delta, execErr
	}
	return resultData, gasUsed, &delta, nil
}

// registerWasmCronLocked adds a cron job for a contract.
func (s *Store) registerWasmCronLocked(contractAddr string, spec wire.WasmCronJobSpec, now int64) error {
	if len(spec.MethodName) == 0 || len(spec.MethodName) > wire.MaxWasmMethodNameLen {
		return errors.New("invalid cron method name")
	}
	if spec.IntervalSeconds < wire.MinWasmCronIntervalSecs {
		return fmt.Errorf("cron interval must be >= %d seconds", wire.MinWasmCronIntervalSecs)
	}

	jobs := s.data.WasmCronJobs[contractAddr]
	if len(jobs) >= wire.MaxWasmCronJobs {
		return fmt.Errorf("max %d cron jobs per contract", wire.MaxWasmCronJobs)
	}

	// Check for duplicate method name.
	for _, j := range jobs {
		if j.MethodName == spec.MethodName {
			return errors.New("duplicate cron method name")
		}
	}

	job := wire.WasmCronJob{
		ContractAddress:    contractAddr,
		MethodName:         spec.MethodName,
		IntervalSeconds:    spec.IntervalSeconds,
		LastExecutedAtUnix: 0,
		NextDueAtUnix:      now + spec.IntervalSeconds,
		Enabled:            true,
		FailureCount:       0,
		CreatedAtUnix:      now,
	}
	s.data.WasmCronJobs[contractAddr] = append(jobs, job)
	return nil
}

// ── Replay-safe apply functions (called during block processing) ──
//
// These apply functions reconstruct state during block replay without
// re-executing WASM bytecode. Financial state changes (fee deduction,
// fund transfers, agent key nonce) are re-applied from payload fields.
// WASM host function side effects are replayed via the WasmStateDelta
// captured during primary execution and stored in each tx payload.

func (s *Store) applyDeployContractLocked(payload deployContractTxPayload, txTime int64) error {
	req := payload.Request
	contractAddr := payload.ContractAddress

	deployer := wire.NormalizeAddress(req.Deployer)

	// Deduct fee.
	account := s.accountLocked(deployer)
	if account.Balance < req.Fee {
		return errors.New("replay: insufficient balance for deploy fee")
	}
	account.Balance -= req.Fee
	account.Nonce++

	// Replay agent key nonce + usage counters (no validation — tx was already validated).
	if requestUsesAgent(req.AgentKeyID) {
		spend := req.Fee + req.InitFund
		s.replayAgentKeyMutationLocked(req.AgentKeyID, spend)
	}

	// Transfer InitFund.
	if req.InitFund > 0 {
		if account.Balance < req.InitFund {
			return errors.New("replay: insufficient balance for init_fund")
		}
		account.Balance -= req.InitFund
		contractAccount := s.accountLocked(contractAddr)
		contractAccount.Balance += req.InitFund
		s.data.Accounts[contractAccount.Address] = contractAccount
	}
	s.data.Accounts[account.Address] = account

	// Store code.
	// Use the exact deploy timestamp from the payload (deterministic),
	// falling back to txTime for blocks produced before DeployedAtUnix was added.
	bytecodeHash := payload.BytecodeHash
	now := payload.DeployedAtUnix
	if now == 0 {
		now = txTime
	}
	if now == 0 {
		now = time.Now().Unix()
	}
	if existing, ok := s.data.WasmCodes[bytecodeHash]; ok {
		existing.RefCount++
	} else {
		decoded, err := base64.StdEncoding.DecodeString(req.BytecodeBase64)
		bytecodeSize := int64(len(req.BytecodeBase64))
		if err == nil {
			bytecodeSize = int64(len(decoded))
		}
		s.data.WasmCodes[bytecodeHash] = &wire.WasmCode{
			Hash:           bytecodeHash,
			BytecodeBase64: req.BytecodeBase64,
			SizeBytes:      bytecodeSize,
			RefCount:       1,
			UploadedAtUnix: now,
		}
	}

	// Create contract record.
	contract := &wire.WasmContract{
		Address:       contractAddr,
		CodeHash:      bytecodeHash,
		Admin:         deployer,
		Label:         req.Label,
		Balance:       req.InitFund,
		Status:        wire.WasmContractStatusActive,
		PublicKV:      req.PublicKV,
		CreatedAtUnix: now,
		UpdatedAtUnix: now,
	}
	s.data.WasmContracts[contractAddr] = contract
	s.data.WasmKVStore[contractAddr] = map[string]string{}

	// Apply WASM state delta from init method execution (before request crons,
	// since the delta captures init's side effects which precede cron registration).
	applyWasmStateDelta(s, contractAddr, payload.StateDelta)

	// Register cron jobs.
	for _, spec := range req.CronJobs {
		s.registerWasmCronLocked(contractAddr, spec, now)
	}

	s.data.WasmNonce++
	return nil
}

func (s *Store) applyCallContractLocked(payload callContractTxPayload, txTime int64) error {
	req := payload.Request
	caller := wire.NormalizeAddress(req.Caller)
	contractAddr := wire.NormalizeAddress(req.ContractAddress)

	// Deduct fee.
	account := s.accountLocked(caller)
	if account.Balance < req.Fee {
		return errors.New("replay: insufficient balance for fee")
	}
	account.Balance -= req.Fee
	account.Nonce++

	// Replay agent key nonce + usage counters (no validation — tx was already validated).
	if requestUsesAgent(req.AgentKeyID) {
		spend := req.Fee + req.Fund
		s.replayAgentKeyMutationLocked(req.AgentKeyID, spend)
	}

	// Transfer fund.
	if req.Fund > 0 {
		if account.Balance < req.Fund {
			return errors.New("replay: insufficient balance for fund")
		}
		account.Balance -= req.Fund
		contract := s.data.WasmContracts[contractAddr]
		if contract != nil {
			contract.Balance += req.Fund
			s.data.WasmContracts[contractAddr] = contract
		}
	}
	s.data.Accounts[account.Address] = account

	// Apply WASM state delta from contract method execution.
	applyWasmStateDelta(s, contractAddr, payload.StateDelta)

	return nil
}

func (s *Store) applyDestroyContractLocked(payload destroyContractTxPayload, txTime int64) error {
	req := payload.Request
	admin := wire.NormalizeAddress(req.Admin)
	contractAddr := wire.NormalizeAddress(req.ContractAddress)

	// Deduct fee.
	account := s.accountLocked(admin)
	if account.Balance < req.Fee {
		return errors.New("replay: insufficient balance for fee")
	}
	account.Balance -= req.Fee
	account.Nonce++

	// Transfer balance.
	contract := s.data.WasmContracts[contractAddr]
	if contract != nil {
		if contract.Balance > 0 {
			account.Balance += contract.Balance
			contract.Balance = 0
		}
		contract.Status = wire.WasmContractStatusDestroyed
		if txTime > 0 {
			contract.UpdatedAtUnix = txTime
		} else {
			contract.UpdatedAtUnix = time.Now().Unix()
		}
		s.data.WasmContracts[contractAddr] = contract
	}
	s.data.Accounts[account.Address] = account

	// Clean up.
	delete(s.data.WasmCronJobs, contractAddr)
	delete(s.data.WasmEventSubscriptions, contractAddr)
	delete(s.data.WasmKVStore, contractAddr)

	// Remove subscriptions targeting this contract from other contracts.
	for subAddr, subs := range s.data.WasmEventSubscriptions {
		filtered := make([]wire.WasmEventSubscription, 0, len(subs))
		for _, sub := range subs {
			if !strings.EqualFold(sub.EmitterAddress, contractAddr) {
				filtered = append(filtered, sub)
			}
		}
		if len(filtered) != len(subs) {
			s.data.WasmEventSubscriptions[subAddr] = filtered
		}
	}

	// Clean up pending events targeting this contract.
	remaining := make([]wire.WasmPendingEventDelivery, 0)
	for _, pending := range s.data.WasmPendingEvents {
		if !strings.EqualFold(pending.Event.EmitterAddress, contractAddr) {
			remaining = append(remaining, pending)
		}
	}
	s.data.WasmPendingEvents = remaining

	// Decrement code ref count.
	if contract != nil {
		if code, ok := s.data.WasmCodes[contract.CodeHash]; ok {
			code.RefCount--
			if code.RefCount <= 0 {
				delete(s.data.WasmCodes, contract.CodeHash)
			}
		}
	}

	return nil
}

func (s *Store) applyWasmCronExecLocked(payload wasmCronExecTxPayload) error {
	p := payload.Payload
	contractAddr := wire.NormalizeAddress(p.ContractAddress)

	contract := s.data.WasmContracts[contractAddr]
	if contract == nil {
		return nil
	}

	// Apply WASM state delta before gas charging and metadata updates,
	// so that the metadata updates below take precedence over delta values.
	applyWasmStateDelta(s, contractAddr, payload.StateDelta)

	// Re-fetch contract after delta application (balance may have changed).
	contract = s.data.WasmContracts[contractAddr]
	if contract == nil {
		return nil
	}

	// Charge gas from contract balance.
	if p.GasCharged > 0 && contract.Balance >= p.GasCharged {
		contract.Balance -= p.GasCharged
		s.data.WasmContracts[contractAddr] = contract
	}

	// Update cron job state.
	jobs := s.data.WasmCronJobs[contractAddr]
	for i, job := range jobs {
		if job.MethodName == p.MethodName {
			if p.Success {
				job.FailureCount = 0
				job.LastExecutedAtUnix = p.BlockTimeUnix
			} else {
				job.FailureCount++
				if job.FailureCount >= wire.WasmCronAutoDisable {
					job.Enabled = false
				}
			}
			job.NextDueAtUnix = p.BlockTimeUnix + job.IntervalSeconds
			jobs[i] = job
			break
		}
	}

	return nil
}

func (s *Store) applyWasmEventDeliveryLocked(payload wasmEventDeliveryTxPayload) error {
	p := payload.Payload
	subscriberAddr := wire.NormalizeAddress(p.SubscriberAddress)

	// Apply WASM state delta before gas charging so that the gas charge
	// below takes precedence over delta's balance changes.
	applyWasmStateDelta(s, subscriberAddr, payload.StateDelta)

	// Charge gas from subscriber balance.
	if p.GasCharged > 0 {
		contract := s.data.WasmContracts[subscriberAddr]
		if contract != nil && contract.Balance >= p.GasCharged {
			contract.Balance -= p.GasCharged
			s.data.WasmContracts[subscriberAddr] = contract
		}
	}

	return nil
}

// MarshalContractEvent is a helper to marshal a contract event for storage.
func MarshalContractEvent(event wire.WasmContractEvent) (json.RawMessage, error) {
	return json.Marshal(event)
}
