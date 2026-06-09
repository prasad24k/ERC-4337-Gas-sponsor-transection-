package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
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

const factoryABIJSON = `[{"constant":true,"inputs":[{"name":"owner","type":"address"},{"name":"salt","type":"uint256"}],"name":"getAddress","outputs":[{"name":"","type":"address"}],"payable":false,"stateMutability":"view","type":"function"}]`

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("WARNING: No .env file found or error reading it")
	}

	mnemonic := os.Getenv("MASTER_MNEMONIC")
	if mnemonic == "" {
		log.Println("WARNING: MASTER_MNEMONIC env variable not set! Using default test mnemonic.")
	}
	passphrase := os.Getenv("MASTER_PASSPHRASE")
	rpcURL := os.Getenv("RPC_URL")
	if rpcURL == "" {
		log.Fatal("ERROR: RPC_URL environment variable is required!")
	}
	factoryAddrStr := os.Getenv("FACTORY_ADDRESS")
	if factoryAddrStr == "" {
		log.Fatal("ERROR: FACTORY_ADDRESS environment variable is required!")
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatalf("Error connecting to RPC: %v", err)
	}
	factoryABI, err := abi.JSON(strings.NewReader(factoryABIJSON))
	if err != nil {
		log.Fatalf("Error parsing factory ABI: %v", err)
	}
	factoryAddr := common.HexToAddress(factoryAddrStr)

	fmt.Println("===================================================")
	if passphrase != "" {
		fmt.Printf("PASSPHRASE USED : (Hidden, length: %d)\n", len(passphrase))
	} else {
		fmt.Println("PASSPHRASE USED : (None)")
	}
	fmt.Printf("RPC URL         : %s\n", rpcURL)
	fmt.Printf("FACTORY ADDRESS : %s\n", factoryAddrStr)
	fmt.Println("===================================================\n")

	seed := bip39.NewSeed(mnemonic, passphrase)
	wallet, err := hdwallet.NewFromSeed(seed)
	if err != nil {
		log.Fatalf("Error creating wallet from seed: %v", err)
	}
	basePath := os.Getenv("BASE_DERIVATION_PATH")
	if basePath == "" {
		log.Fatal("ERROR: BASE_DERIVATION_PATH environment variable is required!")
	}
	
	fmt.Println("Deriving Smart Account Deposit Addresses...")

	var users []UserWallet
	for index := 0; index < 3; index++ {
		pathStr := fmt.Sprintf("%s/%d", basePath, index)
		path := hdwallet.MustParseDerivationPath(pathStr)
		account, err := wallet.Derive(path, false)
		if err != nil {
			log.Fatalf("Error deriving account for index %d: %v", index, err)
		}
		
		ownerAddr := account.Address

		// Setup the call to factory.getAddress(ownerAddr, salt=0)
		salt := big.NewInt(0) // Using salt 0 for default account
		callData, err := factoryABI.Pack("getAddress", ownerAddr, salt)
		if err != nil {
			log.Fatalf("Error packing data: %v", err)
		}

		msg := ethereum.CallMsg{
			To:   &factoryAddr,
			Data: callData,
		}

		result, err := client.CallContract(context.Background(), msg, nil)
		if err != nil {
			log.Fatalf("Error calling factory getAddress for user %d: %v", index, err)
		}

		var smartAccAddr common.Address
		err = factoryABI.UnpackIntoInterface(&smartAccAddr, "getAddress", result)
		if err != nil {
			log.Fatalf("Error unpacking result: %v", err)
		}

		privKey, err := wallet.PrivateKeyHex(account)
		if err != nil {
			log.Fatalf("Error getting private key: %v", err)
		}

		users = append(users, UserWallet{
			UserID:              index,
			OwnerAddress:        ownerAddr.Hex(),
			SmartAccountAddress: smartAccAddr.Hex(),
			DerivationPath:      pathStr,
		})

		fmt.Printf("Generated User ID %d\n", index)
		fmt.Printf(" -> Owner EOA  : %s\n", ownerAddr.Hex())
		fmt.Printf(" -> Deposit SC : %s (Smart Account)\n", smartAccAddr.Hex())
		fmt.Printf(" -> Private Key: %s\n\n", privKey)
	}
	
	jsonData, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		log.Fatalf("Error converting to JSON: %v", err)
	}

	err = os.WriteFile("users.json", jsonData, 0644)
	if err != nil {
		log.Fatalf("Error saving users.json: %v", err)
	}

	fmt.Println("---------------------------------------------------")
	fmt.Println("✅ Addresses and Paths saved to users.json successfully!")
	fmt.Println("❌ Private Keys are NOT stored anywhere.")
}
