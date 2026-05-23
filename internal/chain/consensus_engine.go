package chain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"

	"chain/internal/consensus"
	"chain/internal/wire"
)

const defaultBlockTimeoutMs int64 = 5000
const maxConsensusRounds uint64 = consensus.MaxConsensusRounds

func (s *Store) consensusLocked() consensus.State {
	return consensus.State{
		Height:       s.data.ConsensusHeight,
		Round:        s.data.ConsensusRound,
		Phase:        s.data.ConsensusPhase,
		Proposer:     s.data.ConsensusProposer,
		VotingPower:  s.consensusVotingPowerLocked(),
		TotalPower:   s.consensusTotalPowerLocked(),
		BlockTimeout: consensus.DefaultBlockTimeoutMs,
	}
}

func (s *Store) consensusVotingPowerLocked() uint64 {
	var total uint64
	for _, addr := range s.consensusValidatorAddressesLocked() {
		total += s.validatorPowerLocked(addr)
	}
	return total
}

func (s *Store) consensusTotalPowerLocked() uint64 {
	return s.consensusVotingPowerLocked()
}

func (s *Store) selectProposerLocked(height uint64, round uint64) (string, error) {
	validators := s.consensusValidatorAddressesLocked()
	if len(validators) == 0 {
		return "", errors.New("no consensus validators available")
	}

	powers := make([]uint64, len(validators))
	var totalPower uint64
	for i, addr := range validators {
		p := s.validatorPowerLocked(addr)
		powers[i] = p
		totalPower += p
	}
	if totalPower == 0 {
		return "", errors.New("total voting power is zero")
	}

	seedInput := make([]byte, 0, 8+8+32)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, height)
	seedInput = append(seedInput, buf...)
	binary.BigEndian.PutUint64(buf, round)
	seedInput = append(seedInput, buf...)
	if len(s.data.Blocks) > 0 {
		prevHash := s.data.Blocks[len(s.data.Blocks)-1].Hash
		seedInput = append(seedInput, []byte(prevHash)...)
	}
	hash := sha256.Sum256(seedInput)
	seed := binary.BigEndian.Uint64(hash[:8])

	threshold := seed % totalPower
	var cumulative uint64
	for i, addr := range validators {
		cumulative += powers[i]
		if threshold < cumulative {
			return addr, nil
		}
	}
	return validators[len(validators)-1], nil
}

func (s *Store) advanceConsensusRoundLocked(now int64) {
	cs := &s.data
	if cs.ConsensusPhase == "" {
		cs.ConsensusPhase = "propose"
	}
	if cs.ConsensusHeight == 0 {
		cs.ConsensusHeight = uint64(len(s.data.Blocks)) + 1
	}

	proposer, err := s.selectProposerLocked(cs.ConsensusHeight, cs.ConsensusRound)
	if err != nil {
		return
	}
	cs.ConsensusProposer = proposer
	cs.ConsensusPhase = "propose"

	if s.blockProducer != nil && proposer == s.blockProducer.Address {
		return
	}
}

func (s *Store) transitionConsensusPhaseLocked(nextHeight uint64, now int64) {
	cs := &s.data
	if cs.ConsensusHeight != 0 && cs.ConsensusHeight > nextHeight {
		return
	}

	if cs.ConsensusHeight < nextHeight {
		cs.ConsensusHeight = nextHeight
		cs.ConsensusRound = 0
		cs.ConsensusPhase = "propose"
	}
}

func (s *Store) finalizeConsensusForBlockLocked(block wire.Block) {
	cs := &s.data
	if block.Height > cs.ConsensusHeight {
		cs.ConsensusHeight = block.Height
		cs.ConsensusRound = 0
	}
	if block.Finality.Finalized {
		cs.ConsensusHeight = block.Height + 1
		cs.ConsensusRound = 0
		cs.ConsensusPhase = "propose"
	} else if block.Height == cs.ConsensusHeight {
		cs.ConsensusRound = block.Finality.Round + 1
	}
}

func (s *Store) upgradePlanLocked() consensus.UpgradePlan {
	return s.data.UpgradePlan
}

func (s *Store) SetUpgradePlan(plan consensus.UpgradePlan) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.UpgradePlan = plan
	return s.saveLocked()
}

func (s *Store) UpgradePlan() consensus.UpgradePlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.UpgradePlan
}

func (s *Store) isUpgradeHaltedLocked(height uint64, timeUnix int64) bool {
	plan := s.data.UpgradePlan
	if plan.HaltHeight > 0 && height >= plan.HaltHeight {
		return true
	}
	if plan.HaltTime > 0 && timeUnix >= plan.HaltTime {
		return true
	}
	return false
}

func (s *Store) stateRootLocked() string {
	leaves := make([]string, 0, len(s.data.Accounts))
	for _, account := range s.data.Accounts {
		leaves = append(leaves, hashString("account:"+account.Address+":"+u64toa(account.Balance)+":"+u64toa(account.LockedStorage)+":"+u64toa(account.LockedStake)+":"+u64toa(account.PendingMiningRewards)))
	}
	sort.Strings(leaves)
	return chaincryptoMerkleRoot(leaves)
}

func (s *Store) fullStateRootLocked() string {
	leaves := make([]string, 0)

	for _, account := range s.data.Accounts {
		leaves = append(leaves, hashString("account:"+account.Address+":"+u64toa(account.Balance)+":"+u64toa(account.LockedStorage)+":"+u64toa(account.LockedStake)+":"+u64toa(account.PendingMiningRewards)))
	}
	for _, bucket := range s.data.MiningRewardVestings {
		leaves = append(leaves, hashString("mining_vesting:"+bucket.BucketID+":"+bucket.Address+":"+i64toa(bucket.DayUnix)+":"+u64toa(bucket.Total)+":"+u64toa(bucket.Released)))
	}
	pool := s.data.StorageFeePool
	leaves = append(leaves, hashString("storage_fee_pool:"+u64toa(pool.TotalLocked)+":"+u64toa(pool.TotalPaid)+":"+u64toa(pool.TotalRefunded)+":"+u64toa(pool.TransferredToRewardPool)+":"+u64toa(pool.PermanentFundBalance)+":"+u64toa(pool.InsuranceReserve)))
	for _, escrow := range s.data.DealEscrows {
		leaves = append(leaves, hashString("deal_escrow:"+escrow.IntentID+":"+escrow.User+":"+u64toa(escrow.LockedFee)+":"+u64toa(escrow.PaidFee)+":"+u64toa(escrow.RefundedFee)+":"+u64toa(escrow.AccruedFee)))
	}
	for _, intent := range s.data.Intents {
		leaves = append(leaves, hashString("intent:"+intent.IntentID+":"+intent.Status+":"+intent.StorageStatus))
	}
	for _, miner := range s.data.Miners {
		leaves = append(leaves, hashString("miner:"+miner.MinerAddress+":"+miner.Status+":"+u64toa(miner.ProofSuccess)+":"+u64toa(miner.ProofFailure)+":"+u64toa(miner.Stake)))
	}
	for _, validator := range s.data.Validators {
		leaves = append(leaves, hashString("validator:"+validator.Address+":"+validator.Status+":"+u64toa(validatorPower(validator))))
	}
	for dealFamily, dealID := range s.data.Deals {
		leaves = append(leaves, hashString("deal:"+dealFamily+":"+dealID))
	}

	sort.Strings(leaves)
	return chaincryptoMerkleRoot(leaves)
}

func chaincryptoMerkleRoot(leaves []string) string {
	if len(leaves) == 0 {
		return hashString("empty_state")
	}
	level := make([]string, len(leaves))
	copy(level, leaves)
	for len(level) > 1 {
		next := make([]string, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			left := level[i]
			right := left
			if i+1 < len(level) {
				right = level[i+1]
			}
			h := sha256.New()
			h.Write([]byte(left))
			h.Write([]byte(right))
			next = append(next, hexEncode(h.Sum(nil)))
		}
		level = next
	}
	return level[0]
}

func u64toa(v uint64) string {
	if v == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}

func i64toa(v int64) string {
	if v < 0 {
		return "-" + u64toa(uint64(-v))
	}
	return u64toa(uint64(v))
}

func hexEncode(data []byte) string {
	const hex = "0123456789abcdef"
	buf := make([]byte, len(data)*2)
	for i, b := range data {
		buf[i*2] = hex[b>>4]
		buf[i*2+1] = hex[b&0xf]
	}
	return string(buf)
}

func (s *Store) checkBlockTimeoutLocked(now int64) (bool, uint64) {
	cs := &s.data
	if cs.ConsensusPhase != "propose" {
		return false, cs.ConsensusRound
	}
	expectedHeight := uint64(len(s.data.Blocks)) + 1
	if cs.ConsensusHeight < expectedHeight {
		cs.ConsensusHeight = expectedHeight
		cs.ConsensusRound = 0
		cs.ConsensusPhase = "propose"
		return false, 0
	}
	if cs.ConsensusHeight == expectedHeight {
		return false, cs.ConsensusRound
	}
	return false, cs.ConsensusRound
}
