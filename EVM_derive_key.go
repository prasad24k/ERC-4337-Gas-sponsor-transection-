package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	hdwallet "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/tyler-smith/go-bip39"
)

type UserWallet struct {
	UserID              int    `json:"user_id"`
	OwnerAddress        string `json:"owner_address"`
	SmartAccountAddress string `json:"smart_account_address"`
	DerivationPath      string `json:"derivation_path"`
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("WARNING: No .env file found or error reading it")
	}

	userIDPtr := flag.Int("userid", 1, "")
	flag.Parse()
	fileData, err := os.ReadFile("users.json")
	if err != nil {
		log.Fatalf("Error reading users.json (pehle hdwallet.go run karein): %v", err)
	}
	var users []UserWallet
	if err := json.Unmarshal(fileData, &users); err != nil {
		log.Fatalf("Error parsing users.json: %v", err)
	}
	var targetUser *UserWallet
	for _, u := range users {
		if u.UserID == *userIDPtr {
			targetUser = &u
			break
		}
	}

	if targetUser == nil {
		log.Fatalf("ERROR: User ID %d nahi mila users.json me!", *userIDPtr)
	}
	mnemonic := os.Getenv("MASTER_MNEMONIC")
	if mnemonic == "" {
		log.Fatal("ERROR: MASTER_MNEMONIC environment variable is required!")
	}
	passphrase := os.Getenv("MASTER_PASSPHRASE")
	seed := bip39.NewSeed(mnemonic, passphrase)
	wallet, err := hdwallet.NewFromSeed(seed)
	if err != nil {
		log.Fatalf("Error creating wallet from seed: %v", err)
	}
	path := hdwallet.MustParseDerivationPath(targetUser.DerivationPath)
	account, err := wallet.Derive(path, false)
	if err != nil {
		log.Fatalf("Error deriving account for path %s: %v", targetUser.DerivationPath, err)
	}
	privKey, err := wallet.PrivateKeyHex(account)
	if err != nil {
		log.Fatalf("Error getting private key: %v", err)
	}
	fmt.Println("===================================================")
	fmt.Printf("Target User ID       : %d\n", targetUser.UserID)
	fmt.Printf("Derivation Path      : %s (JSON se loaded)\n", targetUser.DerivationPath)
	fmt.Println("===================================================")
	isMatched := (account.Address.Hex() == targetUser.OwnerAddress)
	fmt.Printf("Owner EOA Address    : %s (Matched with JSON: %v)\n", account.Address.Hex(), isMatched)
	fmt.Printf("Smart Account Address: %s (👈 User yahan deposit karega)\n", targetUser.SmartAccountAddress)
	fmt.Printf("Owner Private Key    : %s (👈 Ye sign karne ke kaam aayegi)\n", privKey)
	fmt.Println("===================================================")
}
