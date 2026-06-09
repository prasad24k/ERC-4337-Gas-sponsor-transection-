package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
	hdwallet "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/tyler-smith/go-bip39"
)

// EntryPoint v0.6 Address
const entryPointAddrStr = "0x5FF137D4b0FDCD49DcA30c7CF57E578a026d2789"

// Mock USDT Token Address on Sepolia
const tokenAddrStr = "0x247EcF45Fe9C61568712AF3D04Cec0722a47c5Ff"

// ABIs for packing data
const entryPointABIJSON = `[{"inputs":[{"components":[{"internalType":"address","name":"sender","type":"address"},{"internalType":"uint256","name":"nonce","type":"uint256"},{"internalType":"bytes","name":"initCode","type":"bytes"},{"internalType":"bytes","name":"callData","type":"bytes"},{"internalType":"uint256","name":"callGasLimit","type":"uint256"},{"internalType":"uint256","name":"verificationGasLimit","type":"uint256"},{"internalType":"uint256","name":"preVerificationGas","type":"uint256"},{"internalType":"uint256","name":"maxFeePerGas","type":"uint256"},{"internalType":"uint256","name":"maxPriorityFeePerGas","type":"uint256"},{"internalType":"bytes","name":"paymasterAndData","type":"bytes"},{"internalType":"bytes","name":"signature","type":"bytes"}],"internalType":"struct UserOperation","name":"userOp","type":"tuple"}],"name":"getUserOpHash","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"},{"inputs":[{"name":"sender","type":"address"},{"name":"key","type":"uint192"}],"name":"getNonce","outputs":[{"name":"nonce","type":"uint256"}],"stateMutability":"view","type":"function"}]`
const factoryABIJSON = `[{"inputs":[{"name":"ownerAddress","type":"address"},{"name":"salt","type":"uint256"}],"name":"createAccount","outputs":[{"name":"ret","type":"address"}],"stateMutability":"nonpayable","type":"function"}]`
const accountABIJSON = `[{"inputs":[{"name":"dest","type":"address"},{"name":"value","type":"uint256"},{"name":"func","type":"bytes"}],"name":"execute","outputs":[],"stateMutability":"nonpayable","type":"function"}]`
const erc20ABIJSON = `[{"inputs":[{"name":"recipient","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"stateMutability":"view","type":"function"},{"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

// UserOperation struct matching the Solidity definition for ABI packing
type UserOperationSol struct {
	Sender               common.Address
	Nonce                *big.Int
	InitCode             []byte
	CallData             []byte
	CallGasLimit         *big.Int
	VerificationGasLimit *big.Int
	PreVerificationGas   *big.Int
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
	PaymasterAndData     []byte
	Signature            []byte
}

// UserOperation struct for Bundler JSON-RPC API payload (hex encoded string values)
type UserOperationJSON struct {
	Sender               string `json:"sender"`
	Nonce                string `json:"nonce"`
	InitCode             string `json:"initCode"`
	CallData             string `json:"callData"`
	CallGasLimit         string `json:"callGasLimit"`
	VerificationGasLimit string `json:"verificationGasLimit"`
	PreVerificationGas   string `json:"preVerificationGas"`
	MaxFeePerGas         string `json:"maxFeePerGas"`
	MaxPriorityFeePerGas string `json:"maxPriorityFeePerGas"`
	PaymasterAndData     string `json:"paymasterAndData"`
	Signature            string `json:"signature"`
}

// JSON-RPC Request payload
type JsonRpcRequest struct {
	JsonRpc string        `json:"jsonrpc"`
	Id      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

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

	userIDStr := os.Getenv("USER_ID")
	userID := 0
	if userIDStr != "" {
		var err error
		userID, err = strconv.Atoi(userIDStr)
		if err != nil {
			log.Fatalf("Invalid USER_ID in env: %v", err)
		}
	}

	rpcURL := os.Getenv("RPC_URL")
	bundlerURL := os.Getenv("BUNDLER_URL")
	factoryAddrStr := os.Getenv("FACTORY_ADDRESS")
	paymasterAddrStr := os.Getenv("PAYMASTER_ADDRESS")
	hotWalletAddrStr := os.Getenv("HOT_WALLET_ADDRESS")
	if hotWalletAddrStr == "" {
		log.Fatal("ERROR: HOT_WALLET_ADDRESS environment variable is required!")
	}
	hotWalletAddr := common.HexToAddress(hotWalletAddrStr)

	// Load users.json
	fileData, err := os.ReadFile("users.json")
	if err != nil {
		log.Fatalf("Error reading users.json (run EVM_hdwallet.go first): %v", err)
	}
	var users []UserWallet
	if err := json.Unmarshal(fileData, &users); err != nil {
		log.Fatalf("Error parsing users.json: %v", err)
	}

	var targetUser *UserWallet
	for _, u := range users {
		if u.UserID == userID {
			targetUser = &u
			break
		}
	}
	if targetUser == nil {
		log.Fatalf("ERROR: User ID %d not found in users.json!", userID)
	}

	// Derive key from mnemonic
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
	ownerPrivKeyHex, err := wallet.PrivateKeyHex(account)
	if err != nil {
		log.Fatalf("Error getting private key: %v", err)
	}

	ownerAddr := account.Address
	smartAccountAddr := common.HexToAddress(targetUser.SmartAccountAddress)

	// 1. Setup client and parse ABIs
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatalf("Error connecting to RPC: %v", err)
	}

	epABI, _ := abi.JSON(strings.NewReader(entryPointABIJSON))
	factoryABI, _ := abi.JSON(strings.NewReader(factoryABIJSON))
	accountABI, _ := abi.JSON(strings.NewReader(accountABIJSON))
	erc20ABI, _ := abi.JSON(strings.NewReader(erc20ABIJSON))

	tokenAddr := common.HexToAddress(tokenAddrStr)

	// 2. Fetch Token Decimals & Balance
	fmt.Println("Querying token decimals and balance...")
	
	decimalsResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddr,
		Data: common.Hex2Bytes("313ce567"), // decimals() selector
	}, nil)
	if err != nil {
		log.Fatalf("Failed to query token decimals: %v", err)
	}
	var decimals uint8
	err = erc20ABI.UnpackIntoInterface(&decimals, "decimals", decimalsResult)
	if err != nil {
		log.Fatalf("Failed to unpack decimals: %v", err)
	}

	balanceData, err := erc20ABI.Pack("balanceOf", smartAccountAddr)
	if err != nil {
		log.Fatalf("Failed to pack balanceOf: %v", err)
	}
	balanceResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddr,
		Data: balanceData,
	}, nil)
	if err != nil {
		log.Fatalf("Failed to query balanceOf: %v", err)
	}
	var tokenBalance *big.Int
	err = erc20ABI.UnpackIntoInterface(&tokenBalance, "balanceOf", balanceResult)
	if err != nil {
		log.Fatalf("Failed to unpack balanceOf: %v", err)
	}

	tokenFloat := new(big.Float).SetInt(tokenBalance)
	divider := new(big.Float).SetFloat64(math.Pow(10, float64(decimals)))
	tokenHuman := new(big.Float).Quo(tokenFloat, divider)

	fmt.Println("===================================================")
	fmt.Printf("Sweeping Token             : %s\n", tokenAddrStr)
	fmt.Printf("Token Decimals             : %d\n", decimals)
	fmt.Printf("Smart Account Balance      : %s (%f USDT)\n", tokenBalance.String(), tokenHuman)
	fmt.Printf("Sweeping from Smart Account: %s\n", smartAccountAddr.Hex())
	fmt.Printf("Destination (Hot Wallet)   : %s\n", hotWalletAddr.Hex())
	fmt.Println("===================================================")

	if tokenBalance.Cmp(big.NewInt(0)) == 0 {
		log.Fatal("ERROR: Token balance is 0. Nothing to sweep!")
	}
	sweepAmount := tokenBalance

	// 3. Fetch Nonce from EntryPoint
	entryPointAddr := common.HexToAddress(entryPointAddrStr)
	var nonce *big.Int

	nonceData, err := epABI.Pack("getNonce", smartAccountAddr, big.NewInt(0))
	if err != nil {
		log.Fatalf("Failed to pack getNonce: %v", err)
	}
	nonceResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &entryPointAddr,
		Data: nonceData,
	}, nil)
	if err != nil {
		log.Printf("Smart Account not deployed yet, defaulting Nonce to 0. (Error: %v)", err)
		nonce = big.NewInt(0)
	} else {
		err = epABI.UnpackIntoInterface(&nonce, "getNonce", nonceResult)
		if err != nil {
			log.Fatalf("Failed to unpack Nonce: %v", err)
		}
	}
	fmt.Printf("Current Nonce: %d\n", nonce)

	// 4. Construct initCode (Only needed if smart account is not deployed yet)
	var initCode []byte
	code, err := client.CodeAt(context.Background(), smartAccountAddr, nil)
	if err != nil || len(code) == 0 {
		fmt.Println("Smart Account NOT deployed yet. Adding initCode to UserOperation...")
		createAccountData, err := factoryABI.Pack("createAccount", ownerAddr, big.NewInt(0))
		if err != nil {
			log.Fatalf("Failed to pack createAccount: %v", err)
		}

		factoryAddr := common.HexToAddress(factoryAddrStr)
		initCode = append(factoryAddr.Bytes(), createAccountData...)
	} else {
		fmt.Println("Smart Account ALREADY deployed. initCode set to 0x")
		initCode = []byte{}
	}

	// 5. Construct callData (To execute transfer call on ERC-20 token)
	// We want the Smart Account to execute: token.transfer(HotWallet, sweepAmount)
	transferCalldata, err := erc20ABI.Pack("transfer", hotWalletAddr, sweepAmount)
	if err != nil {
		log.Fatalf("Failed to pack transfer: %v", err)
	}

	// Smart Account execute(dest = tokenAddress, value = 0, funcData = transferCalldata)
	executeData, err := accountABI.Pack("execute", tokenAddr, big.NewInt(0), transferCalldata)
	if err != nil {
		log.Fatalf("Failed to pack execute: %v", err)
	}

	// 6. Construct paymasterAndData
	paymasterAddr := common.HexToAddress(paymasterAddrStr)
	paymasterAndData := paymasterAddr.Bytes()

	// 7. Set Gas prices (Suggest from network)
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		log.Fatalf("Failed to suggest gas price: %v", err)
	}
	maxFeePerGas := new(big.Int).Mul(gasPrice, big.NewInt(3)) // 3x to ensure it passes on testnet
	maxPriorityFeePerGas := big.NewInt(2000000000)            // 2.0 Gwei

	// 8. Define Gas limits (ERC-20 transfers need slightly more verification gas limit than native ETH transfers)
	callGasLimit := big.NewInt(150000)
	verificationGasLimit := big.NewInt(1500000)
	preVerificationGas := big.NewInt(150000)

	// 9. Build UserOperation struct
	userOp := UserOperationSol{
		Sender:               smartAccountAddr,
		Nonce:                nonce,
		InitCode:             initCode,
		CallData:             executeData,
		CallGasLimit:         callGasLimit,
		VerificationGasLimit: verificationGasLimit,
		PreVerificationGas:   preVerificationGas,
		MaxFeePerGas:         maxFeePerGas,
		MaxPriorityFeePerGas: maxPriorityFeePerGas,
		PaymasterAndData:     paymasterAndData,
		Signature:            []byte{}, // Empty signature before hashing
	}

	// 10. Fetch userOpHash from EntryPoint
	hashData, err := epABI.Pack("getUserOpHash", userOp)
	if err != nil {
		log.Fatalf("Failed to pack getUserOpHash: %v", err)
	}
	hashResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &entryPointAddr,
		Data: hashData,
	}, nil)
	if err != nil {
		log.Fatalf("Failed to call getUserOpHash: %v", err)
	}

	var userOpHash [32]byte
	err = epABI.UnpackIntoInterface(&userOpHash, "getUserOpHash", hashResult)
	if err != nil {
		log.Fatalf("Failed to unpack userOpHash: %v", err)
	}
	fmt.Printf("Generated UserOperation Hash: %s\n", hexutil.Encode(userOpHash[:]))

	// 11. Sign the UserOperation Hash (using owner private key)
	privateKey, err := crypto.HexToECDSA(ownerPrivKeyHex)
	if err != nil {
		log.Fatalf("Failed to parse private key: %v", err)
	}

	prefixedHash := crypto.Keccak256(
		[]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n32%s", string(userOpHash[:]))),
	)

	signature, err := crypto.Sign(prefixedHash, privateKey)
	if err != nil {
		log.Fatalf("Failed to sign hash: %v", err)
	}

	if signature[64] < 27 {
		signature[64] += 27
	}

	userOp.Signature = signature
	fmt.Printf("Signature generated successfully: %s\n", hexutil.Encode(signature))

	// 12. Format payload to JSON-RPC format for Bundler
	userOpJSON := UserOperationJSON{
		Sender:               userOp.Sender.Hex(),
		Nonce:                hexutil.EncodeBig(userOp.Nonce),
		InitCode:             hexutil.Encode(userOp.InitCode),
		CallData:             hexutil.Encode(userOp.CallData),
		CallGasLimit:         hexutil.EncodeBig(userOp.CallGasLimit),
		VerificationGasLimit: hexutil.EncodeBig(userOp.VerificationGasLimit),
		PreVerificationGas:   hexutil.EncodeBig(userOp.PreVerificationGas),
		MaxFeePerGas:         hexutil.EncodeBig(userOp.MaxFeePerGas),
		MaxPriorityFeePerGas: hexutil.EncodeBig(userOp.MaxPriorityFeePerGas),
		PaymasterAndData:     hexutil.Encode(userOp.PaymasterAndData),
		Signature:            hexutil.Encode(userOp.Signature),
	}

	rpcRequest := JsonRpcRequest{
		JsonRpc: "2.0",
		Id:      1,
		Method:  "eth_sendUserOperation",
		Params:  []interface{}{userOpJSON, entryPointAddrStr},
	}

	jsonBytes, err := json.Marshal(rpcRequest)
	if err != nil {
		log.Fatalf("Failed to marshal JSON payload: %v", err)
	}

	// 13. Send POST request to Bundler
	resp, err := http.Post(bundlerURL, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		log.Fatalf("HTTP Post to bundler failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var prettyJSON bytes.Buffer
	errorPretty := json.Indent(&prettyJSON, body, "", "  ")
	if errorPretty == nil {
		fmt.Printf("\nBundler Response:\n%s\n", prettyJSON.String())
	} else {
		fmt.Printf("\nBundler Response:\n%s\n", string(body))
	}
}
