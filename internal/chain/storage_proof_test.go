package chain

import (
	"crypto/ecdsa"
	"encoding/base64"
	"testing"
	"time"

	chaincrypto "chain/internal/crypto"
	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestSubmitProofRequiresAllChallengeSamples(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	minerAddress := wire.AccountAddress(&key.PublicKey)
	minerPublicKey := wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey))
	store.data.Miners[minerAddress] = wire.MinerStats{
		MinerAddress: minerAddress,
		PublicKey:    minerPublicKey,
		Stake:        100,
		Status:       "active",
	}
	store.data.Accounts[minerAddress] = wire.Account{Address: minerAddress, LockedStake: 100}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: 10}

	data := make([]byte, chaincrypto.DefaultLeafSize*5)
	for i := range data {
		data[i] = byte(i % 251)
	}
	shardHash := chaincrypto.HashBytes(data)
	sectorRoot := chaincrypto.DataMerkleRoot(data, chaincrypto.DefaultLeafSize)
	challenge := wire.StorageChallenge{
		ChallengeID:      "challenge_multi",
		ProofType:        proofTypeMerklePORV1,
		IntentID:         "intent_multi",
		DealID:           "deal_multi",
		ShardHash:        shardHash,
		ShardSize:        int64(len(data)),
		SectorCommitment: sectorRoot,
		LeafSize:         chaincrypto.DefaultLeafSize,
		LeafIndices:      []int{0, 2, 4},
		SampleCount:      3,
		MinerAddress:     minerAddress,
		MinerPublicKey:   minerPublicKey,
		Nonce:            "nonce_multi",
		ChallengeSeed:    hashString("intent_multi:" + shardHash + ":nonce_multi"),
		ExpiresAtUnix:    time.Now().Add(time.Minute).Unix(),
		Reward:           7,
	}
	challenge.LeafIndex = challenge.LeafIndices[0]
	challenge.LeafRanges = challengeLeafRanges(challenge.ShardSize, challenge.LeafSize, challenge.LeafIndices)
	challenge.ChallengeHash = storageChallengeHash(challenge)
	store.data.Intents[challenge.IntentID] = &Intent{IntentView: wire.IntentView{
		IntentID:      challenge.IntentID,
		User:          "alice",
		FileSize:      int64(len(data)),
		Status:        wire.StatusFinalized,
		StorageStatus: wire.StorageStatusActive,
		LockedFee:     10,
		Policy:        wire.StoragePolicy{Duration: 10},
	},
		CreatedAt: time.Now().Add(-20 * time.Second).Unix(),
		UpdatedAt: time.Now().Add(-20 * time.Second).Unix(),
	}
	store.data.Challenges[challenge.ChallengeID] = challenge

	proof := multiSampleProof(t, data, challenge, minerPublicKey, minerAddress, key)
	resp, err := store.SubmitProof(wire.SubmitProofRequest{Proof: proof})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "accepted" || resp.Reward != 7 {
		t.Fatalf("unexpected submit response %+v", resp)
	}
	if user := store.accountLocked("alice"); user.LockedStorage != 3 {
		t.Fatalf("expected user locked storage 3, got %d", user.LockedStorage)
	}
	if miner := store.accountLocked(minerAddress); miner.Balance != 0 {
		t.Fatalf("expected miner balance 0 before vesting release, got %d", miner.Balance)
	} else if miner.PendingMiningRewards != 7 {
		t.Fatalf("expected miner pending mining rewards 7, got %d", miner.PendingMiningRewards)
	}
	if stats := store.minerStatsLocked(minerAddress); stats.Rewards != 7 || stats.ProofSuccess != 1 {
		t.Fatalf("unexpected miner stats %+v", stats)
	}

	tampered := proof
	tampered.ChallengeID = "challenge_multi_tampered"
	challenge.ChallengeID = tampered.ChallengeID
	store.data.Challenges[challenge.ChallengeID] = challenge
	tampered.LeafHashes = tampered.LeafHashes[:2]
	tampered.MerklePaths = tampered.MerklePaths[:2]
	tampered.ProofHash = expectedProofHash(challenge, tampered)
	if err := wire.SignProof(&tampered, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitProof(wire.SubmitProofRequest{Proof: tampered}); err == nil {
		t.Fatal("expected proof with missing sample to be rejected")
	}
}

func TestSubmitProofRewardIsCappedByLockedStorage(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	minerAddress := wire.AccountAddress(&key.PublicKey)
	minerPublicKey := wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey))
	store.data.Miners[minerAddress] = wire.MinerStats{
		MinerAddress: minerAddress,
		PublicKey:    minerPublicKey,
		Stake:        100,
		Status:       "active",
	}
	store.data.Accounts[minerAddress] = wire.Account{Address: minerAddress, LockedStake: 100}
	store.data.Accounts["alice"] = wire.Account{Address: "alice", LockedStorage: 4}

	data := make([]byte, chaincrypto.DefaultLeafSize*2)
	for i := range data {
		data[i] = byte(i % 197)
	}
	challenge := wire.StorageChallenge{
		ChallengeID:      "challenge_capped_reward",
		ProofType:        proofTypeMerklePORV1,
		IntentID:         "intent_capped_reward",
		DealID:           "deal_capped_reward",
		ShardHash:        chaincrypto.HashBytes(data),
		ShardSize:        int64(len(data)),
		SectorCommitment: chaincrypto.DataMerkleRoot(data, chaincrypto.DefaultLeafSize),
		LeafSize:         chaincrypto.DefaultLeafSize,
		LeafIndex:        0,
		LeafIndices:      []int{0, 1},
		SampleCount:      2,
		MinerAddress:     minerAddress,
		MinerPublicKey:   minerPublicKey,
		Nonce:            "nonce_capped_reward",
		ChallengeSeed:    hashString("intent_capped_reward:" + chaincrypto.HashBytes(data) + ":nonce_capped_reward"),
		ExpiresAtUnix:    time.Now().Add(time.Minute).Unix(),
		Reward:           9,
	}
	challenge.LeafRanges = challengeLeafRanges(challenge.ShardSize, challenge.LeafSize, challenge.LeafIndices)
	challenge.ChallengeHash = storageChallengeHash(challenge)
	store.data.Intents[challenge.IntentID] = &Intent{IntentView: wire.IntentView{
		IntentID:      challenge.IntentID,
		User:          "alice",
		FileSize:      int64(len(data)),
		Status:        wire.StatusFinalized,
		StorageStatus: wire.StorageStatusActive,
		LockedFee:     4,
		Policy:        wire.StoragePolicy{Duration: 10},
	},
		CreatedAt: time.Now().Add(-20 * time.Second).Unix(),
		UpdatedAt: time.Now().Add(-20 * time.Second).Unix(),
	}
	store.data.Challenges[challenge.ChallengeID] = challenge

	proof := multiSampleProof(t, data, challenge, minerPublicKey, minerAddress, key)
	resp, err := store.SubmitProof(wire.SubmitProofRequest{Proof: proof})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reward != 9 {
		t.Fatalf("expected full reward 9 with storage pool subsidy, got %d", resp.Reward)
	}
	if user := store.accountLocked("alice"); user.LockedStorage != 0 {
		t.Fatalf("expected user locked storage 0, got %d", user.LockedStorage)
	}
	if miner := store.accountLocked(minerAddress); miner.Balance != 0 {
		t.Fatalf("expected miner balance 0 before vesting release, got %d", miner.Balance)
	} else if miner.PendingMiningRewards != 9 {
		t.Fatalf("expected miner pending mining rewards 9, got %d", miner.PendingMiningRewards)
	}
}

func TestStorageProofSampleCountScalesWithShardSize(t *testing.T) {
	samples := DefaultMiningParams().StorageProofSamples
	small := storageProofSampleCount(chaincrypto.DefaultLeafSize*2, chaincrypto.DefaultLeafSize, samples)
	large := storageProofSampleCount(chaincrypto.DefaultLeafSize*4096, chaincrypto.DefaultLeafSize, samples)
	huge := storageProofSampleCount(1<<62, chaincrypto.DefaultLeafSize, samples)
	if small != 2 {
		t.Fatalf("expected small shard to sample all leaves, got %d", small)
	}
	if large <= samples {
		t.Fatalf("expected large shard sample count to grow past default, got %d", large)
	}
	if huge != 64 {
		t.Fatalf("expected huge shard sample count cap 64, got %d", huge)
	}
}

func multiSampleProof(t *testing.T, data []byte, challenge wire.StorageChallenge, minerPublicKey string, minerAddress string, privateKey *ecdsa.PrivateKey) wire.StorageProof {
	t.Helper()
	leafHashes := make([]string, 0, len(challenge.LeafIndices))
	leafPayloads := make([]string, 0, len(challenge.LeafIndices))
	paths := make([][]string, 0, len(challenge.LeafIndices))
	for _, index := range challenge.LeafIndices {
		proof, err := chaincrypto.BuildMerkleProof(data, challenge.LeafSize, index)
		if err != nil {
			t.Fatal(err)
		}
		start := index * challenge.LeafSize
		end := start + challenge.LeafSize
		if end > len(data) {
			end = len(data)
		}
		payload := data[start:end]
		leafHashes = append(leafHashes, proof.LeafHash)
		leafPayloads = append(leafPayloads, base64.StdEncoding.EncodeToString(payload))
		paths = append(paths, proof.Path)
	}
	proof := wire.StorageProof{
		ChallengeID:        challenge.ChallengeID,
		ProofType:          challenge.ProofType,
		ChallengeHash:      challenge.ChallengeHash,
		MinerAddress:       minerAddress,
		MinerPublicKey:     minerPublicKey,
		ShardHash:          challenge.ShardHash,
		ShardSize:          challenge.ShardSize,
		SectorCommitment:   challenge.SectorCommitment,
		LeafSize:           challenge.LeafSize,
		LeafIndex:          challenge.LeafIndices[0],
		LeafIndices:        append([]int(nil), challenge.LeafIndices...),
		LeafHash:           leafHashes[0],
		LeafHashes:         leafHashes,
		LeafDataBase64:     leafPayloads[0],
		LeafPayloadsBase64: leafPayloads,
		MerklePath:         paths[0],
		MerklePaths:        paths,
	}
	proof.ProofHash = expectedProofHash(challenge, proof)
	if err := wire.SignProof(&proof, privateKey); err != nil {
		t.Fatal(err)
	}
	return proof
}
