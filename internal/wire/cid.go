package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
)

func RawCIDForBytes(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	return RawCIDForHash(hex.EncodeToString(sum[:]))
}

func RawCIDForHash(hash string) (string, error) {
	digest, err := hex.DecodeString(strings.TrimPrefix(hash, "0x"))
	if err != nil {
		return "", err
	}
	multihash, err := mh.Encode(digest, mh.SHA2_256)
	if err != nil {
		return "", err
	}
	return cid.NewCidV1(cid.Raw, multihash).String(), nil
}

func HashFromRawCID(value string) (string, error) {
	decoded, err := cid.Decode(value)
	if err != nil {
		return "", err
	}
	digest, err := mh.Decode(decoded.Hash())
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Digest), nil
}
