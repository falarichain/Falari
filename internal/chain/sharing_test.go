package chain

import (
	"testing"

	"chain/internal/wire"
)

func TestCreateAddressAndPasscodeShares(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	intent := testLifecycleIntent()
	intent.Encryption = &wire.EncryptionMetadata{
		Algorithm:            "AES-256-GCM/segment-v1",
		KeyHash:              "key_hash",
		NonceBase64:          "nonce",
		PlaintextSize:        32,
		PlaintextSegmentSize: 32,
	}
	intent.AccessStatus = wire.AccessStatusPrivate
	store.data.Intents[intent.IntentID] = intent

	addressShare, err := store.CreateAddressShare(wire.CreateAddressShareRequest{
		IntentID:         intent.IntentID,
		Owner:            intent.User,
		Recipient:        "bob",
		Algorithm:        "x25519-aeskw-v1",
		EncryptedDataKey: "encrypted_for_bob",
		Nonce:            "nonce_bob",
	})
	if err != nil {
		t.Fatal(err)
	}
	if addressShare.Share.Mode != wire.ShareModeAddress {
		t.Fatalf("unexpected address share mode: %+v", addressShare.Share)
	}
	if addressShare.Envelope.Recipient != "bob" || addressShare.Envelope.RecipientType != wire.KeyEnvelopeRecipientAddress {
		t.Fatalf("unexpected address envelope: %+v", addressShare.Envelope)
	}

	passcodeShare, err := store.CreatePasscodeShare(wire.CreatePasscodeShareRequest{
		IntentID:         intent.IntentID,
		Owner:            intent.User,
		Mode:             wire.ShareModeLinkFragment,
		Algorithm:        "AES-256-GCM/passcode-wrap-v1",
		EncryptedDataKey: "encrypted_for_code",
		Nonce:            "nonce_code",
		KDF: &wire.PasscodeKDFParams{
			Name:       "PBKDF2-SHA256/passcode-v1",
			Salt:       "salt",
			Iterations: 310000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if passcodeShare.Share.Mode != wire.ShareModeLinkFragment {
		t.Fatalf("unexpected passcode share mode: %+v", passcodeShare.Share)
	}
	if passcodeShare.Envelope.KDF == nil || passcodeShare.Envelope.KDF.Salt != "salt" {
		t.Fatalf("expected kdf params on passcode envelope: %+v", passcodeShare.Envelope)
	}

	listed, err := store.ListShares(intent.IntentID, intent.User, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Shares) != 2 || len(listed.Envelopes) != 2 {
		t.Fatalf("expected two shares and envelopes, got %+v", listed)
	}

	if err := store.RevokeShare(wire.RevokeShareRequest{ShareID: addressShare.Share.ShareID, Owner: intent.User}); err != nil {
		t.Fatal(err)
	}
	active, err := store.ListShares(intent.IntentID, intent.User, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Shares) != 1 {
		t.Fatalf("expected one active share after revoke, got %+v", active)
	}
}

func TestAddressShareRecipientIsCaseInsensitive(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	intent := testLifecycleIntent()
	intent.Encryption = &wire.EncryptionMetadata{
		Algorithm:            "AES-256-GCM/segment-v1",
		KeyHash:              "key_hash",
		NonceBase64:          "nonce",
		PlaintextSize:        32,
		PlaintextSegmentSize: 32,
	}
	intent.AccessStatus = wire.AccessStatusPrivate
	store.data.Intents[intent.IntentID] = intent

	mixedCaseRecipient := "0xAbCDEFabcdefABCDefabCDefABcdefabCDEFABCD"
	addressShare, err := store.CreateAddressShare(wire.CreateAddressShareRequest{
		IntentID:         intent.IntentID,
		Owner:            intent.User,
		Recipient:        mixedCaseRecipient,
		Algorithm:        "AES-256-GCM/address-link-wrap-v1",
		EncryptedDataKey: "encrypted_for_address",
		Nonce:            "nonce_address",
		KDF: &wire.PasscodeKDFParams{
			Name:       "PBKDF2-SHA256/address-link-v1",
			Salt:       "salt",
			Iterations: 310000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectedRecipient := wire.NormalizeAddress(mixedCaseRecipient)
	if addressShare.Share.Recipient != expectedRecipient {
		t.Fatalf("expected normalized recipient %s, got %s", expectedRecipient, addressShare.Share.Recipient)
	}

	upperCaseRecipient := "0XABCDEFABCDEFABCDEFABCDEFABCDEFABCDEFABCD"
	listed, err := store.ListShares(intent.IntentID, "", upperCaseRecipient, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Shares) != 1 {
		t.Fatalf("expected mixed-case recipient lookup to find one share, got %+v", listed)
	}
}

func TestCreateShareRejectsNonOwnerAndPlainIntent(t *testing.T) {
	store, err := OpenStore("")
	if err != nil {
		t.Fatal(err)
	}
	intent := testLifecycleIntent()
	store.data.Intents[intent.IntentID] = intent

	if _, err := store.CreateAddressShare(wire.CreateAddressShareRequest{
		IntentID:         intent.IntentID,
		Owner:            intent.User,
		Recipient:        "bob",
		Algorithm:        "x25519-aeskw-v1",
		EncryptedDataKey: "encrypted_for_bob",
	}); err == nil {
		t.Fatal("expected plain intent share to be rejected")
	}

	intent.Encryption = &wire.EncryptionMetadata{
		Algorithm:            "AES-256-GCM/segment-v1",
		KeyHash:              "key_hash",
		NonceBase64:          "nonce",
		PlaintextSize:        32,
		PlaintextSegmentSize: 32,
	}
	store.data.Intents[intent.IntentID] = intent
	if _, err := store.CreateAddressShare(wire.CreateAddressShareRequest{
		IntentID:         intent.IntentID,
		Owner:            "mallory",
		Recipient:        "bob",
		Algorithm:        "x25519-aeskw-v1",
		EncryptedDataKey: "encrypted_for_bob",
	}); err == nil {
		t.Fatal("expected non-owner share to be rejected")
	}
}
