package crypto

import "testing"

func TestMerkleProofVerifiesLeaf(t *testing.T) {
	data := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
	proof, err := BuildMerkleProof(data, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyMerkleProof(proof.Root, proof.LeafHash, proof.LeafIndex, proof.Path) {
		t.Fatal("expected merkle proof to verify")
	}
	if VerifyMerkleProof(proof.Root, HashBytes([]byte("tampered")), proof.LeafIndex, proof.Path) {
		t.Fatal("expected tampered leaf to fail")
	}
}
