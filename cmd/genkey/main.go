package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"chain/internal/chain"
)

func main() {
	out := flag.String("out", "validator.json", "output validator identity file")
	flag.Parse()

	identity, err := chain.LoadOrCreateValidatorIdentity(*out)
	if err != nil {
		log.Fatalf("create validator identity: %v", err)
	}
	fmt.Printf("address:    %s\n", identity.Address)
	fmt.Printf("public_key: %s\n", identity.PublicKeyBase64())
	fmt.Printf("file:       %s\n", *out)

	_ = json.NewEncoder(os.Stdout)
}
