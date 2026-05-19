package chain

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestPeerNetworkSyncOnceFetchesMissingBlocks(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetBlockProducer(identity)
	if _, err := producer.Faucet(wire.FaucetRequest{Address: "alice", Amount: 100}); err != nil {
		t.Fatal(err)
	}
	first, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.Transfer(wire.TransferRequest{From: "alice", To: "bob", Amount: 30}); err != nil {
		t.Fatal(err)
	}
	second, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	blocks := []wire.Block{first.Block, second.Block}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/blocks/latest":
			writeJSON(w, http.StatusOK, wire.BlockResponse{Block: blocks[len(blocks)-1]})
		case strings.HasPrefix(r.URL.Path, "/blocks/"):
			rawHeight := strings.TrimPrefix(r.URL.Path, "/blocks/")
			height, err := strconv.Atoi(rawHeight)
			if err != nil || height <= 0 || height > len(blocks) {
				writeError(w, http.StatusNotFound, http.ErrNoLocation)
				return
			}
			writeJSON(w, http.StatusOK, wire.BlockResponse{Block: blocks[height-1]})
		default:
			writeError(w, http.StatusNotFound, http.ErrNoLocation)
		}
	}))
	defer server.Close()

	peer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	network := NewPeerNetwork(peer, server.URL)
	network.SyncOnce()

	if peer.Height() != 2 {
		t.Fatalf("expected peer height 2, got %d", peer.Height())
	}
	if peer.accountLocked("alice").Balance != 70 {
		t.Fatalf("expected alice balance 70, got %d", peer.accountLocked("alice").Balance)
	}
	if peer.accountLocked("bob").Balance != 30 {
		t.Fatalf("expected bob balance 30, got %d", peer.accountLocked("bob").Balance)
	}
}

func TestLibP2PGossipBroadcastsBlocks(t *testing.T) {
	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-libp2p", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetBlockProducer(identity)

	producerNetwork, err := NewPeerNetworkWithConfig(producer, PeerNetworkConfig{
		LibP2PListen: "/ip4/127.0.0.1/tcp/0",
		GossipTopic:  "storage-chain/test-libp2p-blocks",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer producerNetwork.Close()
	producer.SetBlockBroadcaster(producerNetwork)
	producer.SetTransactionBroadcaster(producerNetwork)

	peerStore, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	peerNetwork, err := NewPeerNetworkWithConfig(peerStore, PeerNetworkConfig{
		LibP2PListen: "/ip4/127.0.0.1/tcp/0",
		LibP2PPeers:  strings.Join(producerNetwork.LibP2PAddrs(), ","),
		GossipTopic:  "storage-chain/test-libp2p-blocks",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer peerNetwork.Close()

	waitFor(t, 5*time.Second, func() bool {
		return len(producerNetwork.host.Network().Peers()) > 0 && len(peerNetwork.host.Network().Peers()) > 0
	})
	time.Sleep(750 * time.Millisecond)

	if _, err := producer.Faucet(wire.FaucetRequest{Address: "alice", Amount: 100}); err != nil {
		t.Fatal(err)
	}
	produced, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected producer block")
	}

	waitFor(t, 10*time.Second, func() bool {
		return peerStore.Height() == produced.Block.Height
	})
	if peerStore.accountLocked("alice").Balance != 100 {
		t.Fatalf("expected libp2p gossip block to replay alice balance 100, got %d", peerStore.accountLocked("alice").Balance)
	}
}

func TestSignedTransferNonce(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	if _, err := store.Faucet(wire.FaucetRequest{Address: from, Amount: 100}); err != nil {
		t.Fatal(err)
	}
	req := wire.TransferRequest{
		From:   from,
		To:     "bob",
		Amount: 40,
		Fee:    1,
		Nonce:  0,
	}
	if err := wire.SignTransfer(&req, privateKey); err != nil {
		t.Fatal(err)
	}
	resp, err := store.Transfer(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.From.Nonce != 1 || resp.From.Balance != 60 || resp.To.Balance != 40 {
		t.Fatalf("unexpected signed transfer state: from=%+v to=%+v", resp.From, resp.To)
	}
	if _, err := store.Transfer(req); err == nil {
		t.Fatal("expected repeated signed transfer nonce to be rejected")
	}
}

func TestSignedRawEthereumTransferNonce(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := wire.AccountAddress(&privateKey.PublicKey)
	if _, err := store.Faucet(wire.FaucetRequest{Address: from, Amount: 100}); err != nil {
		t.Fatal(err)
	}
	resp, err := store.Transfer(wire.TransferRequest{
		From:   from,
		To:     "0x00000000000000000000000000000000000000b0",
		Amount: 25,
		Fee:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.From.Nonce != 0 || resp.From.Balance != 75 || resp.To.Balance != 25 {
		t.Fatalf("unexpected transfer state: from=%+v to=%+v", resp.From, resp.To)
	}
	lowercaseRecipient, err := store.Account("0x00000000000000000000000000000000000000b0")
	if err != nil {
		t.Fatal(err)
	}
	if lowercaseRecipient.Balance != 25 {
		t.Fatalf("expected normalized Ethereum address lookup balance 25, got %d", lowercaseRecipient.Balance)
	}
	if _, err := store.SubmitRawTransaction(wire.RawTransactionRequest{RawTx: "0x"}); err == nil {
		t.Fatal("expected raw transaction to be rejected after EVM removal")
	}
}

func TestAcceptedTransactionIsAppliedWhenProduced(t *testing.T) {
	source, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Faucet(wire.FaucetRequest{Address: "alice", Amount: 100}); err != nil {
		t.Fatal(err)
	}
	tx := source.Mempool().Pending[0]

	producer, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreateValidatorIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := identity.RegistrationRequest("http://validator-b", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.RegisterValidator(registration); err != nil {
		t.Fatal(err)
	}
	producer.SetBlockProducer(identity)

	accepted, err := producer.AcceptTransaction(tx)
	if err != nil {
		t.Fatal(err)
	}
	if !accepted {
		t.Fatal("expected gossiped transaction to be accepted")
	}
	if producer.accountLocked("alice").Balance != 0 {
		t.Fatal("gossiped transaction should not execute before block production")
	}
	produced, err := producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !produced.Produced {
		t.Fatal("expected block to be produced")
	}
	if producer.accountLocked("alice").Balance != 100 {
		t.Fatalf("expected gossiped transaction to execute during production, got balance %d", producer.accountLocked("alice").Balance)
	}
	if !producer.data.ConfirmedTxs[tx.TxID] {
		t.Fatal("gossiped transaction should be confirmed after block production")
	}
}

func waitFor(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
