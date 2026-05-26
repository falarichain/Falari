package main

import (
	"fmt"
	"log"

	"chain/internal/wire"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func main() {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		log.Fatalf("generate key: %v", err)
	}
	privHex := wire.EncodeHex(ethcrypto.FromECDSA(key))
	pubHex := wire.EncodeHex(ethcrypto.CompressPubkey(&key.PublicKey))
	addr := wire.AccountAddress(&key.PublicKey)

	fmt.Println("ECDSA secp256k1 key generated")
	fmt.Println()
	fmt.Printf("address:      %s\n", addr)
	fmt.Printf("public_key:   %s\n", pubHex)
	fmt.Printf("private_key:  %s\n", privHex)
	fmt.Println()
	fmt.Println("Store the private key securely. Use it via environment variable:")
	fmt.Println("  VALIDATOR_PRIVATE_KEY=<private_key> chainnode ...")
	fmt.Println("  MINER_PRIVATE_KEY=<private_key>     storagenode ...")
	fmt.Println("  MINER_PRIVATE_KEY=<private_key>     retrievalnode ...")
}
