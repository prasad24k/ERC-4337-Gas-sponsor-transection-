package main

import (
	"context"
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
)

const entryPointAddrStr = "0x5FF137D4b0FDCD49DcA30c7CF57E578a026d2789"
const entryPointABIJSON = `[{"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

// ABIs for factory and paymaster getters
const factoryABIJSON = `[{"inputs":[],"name":"paymaster","outputs":[{"name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[{"name":"ownerAddress","type":"address"},{"name":"salt","type":"uint256"}],"name":"createAccount","outputs":[{"name":"ret","type":"address"}],"stateMutability":"nonpayable","type":"function"}]`
const paymasterABIJSON = `[{"inputs":[],"name":"factory","outputs":[{"name":"","type":"address"}],"stateMutability":"view","type":"function"}]`

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("WARNING: No .env file found or error reading it")
	}

	rpcURL := os.Getenv("RPC_URL")
	if rpcURL == "" {
		log.Fatal("ERROR: RPC_URL environment variable is required!")
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatalf("Error connecting to RPC: %v", err)
	}

	epABI, _ := abi.JSON(strings.NewReader(entryPointABIJSON))
	factoryABI, _ := abi.JSON(strings.NewReader(factoryABIJSON))
	paymasterABI, _ := abi.JSON(strings.NewReader(paymasterABIJSON))

	entryPointAddr := common.HexToAddress(entryPointAddrStr)

	// Addresses from env
	factoryAddrStr := os.Getenv("FACTORY_ADDRESS")
	paymasterAddrStr := os.Getenv("PAYMASTER_ADDRESS")
	
	factoryAddr := common.HexToAddress(factoryAddrStr)
	paymasterAddr := common.HexToAddress(paymasterAddrStr)
	smartAccountAddr := common.HexToAddress("0x4d155749c75D639C8ecAbd3aEd04584D60C7661C")
	ownerAddr := common.HexToAddress("0x17769439EB0CC10a095C810029E20a9089CCD339")

	fmt.Println("===================================================")
	fmt.Println("🔍 CHECKING SYSTEM CONFIGURATION & BALANCES")
	fmt.Println("===================================================")

	// 1. Check Paymaster Deposit Balance in EntryPoint
	balanceData, err := epABI.Pack("balanceOf", paymasterAddr)
	if err == nil {
		result, err := client.CallContract(context.Background(), ethereum.CallMsg{
			To:   &entryPointAddr,
			Data: balanceData,
		}, nil)
		if err == nil {
			var paymasterBalance *big.Int
			epABI.UnpackIntoInterface(&paymasterBalance, "balanceOf", result)
			paymasterETH := new(big.Float).Quo(new(big.Float).SetInt(paymasterBalance), big.NewFloat(1e18))
			fmt.Printf("Paymaster Deposit Balance in EntryPoint: %f ETH\n", paymasterETH)
		}
	}

	// 2. Check Smart Account Native ETH Balance
	saBalance, err := client.BalanceAt(context.Background(), smartAccountAddr, nil)
	if err == nil {
		saETH := new(big.Float).Quo(new(big.Float).SetInt(saBalance), big.NewFloat(1e18))
		fmt.Printf("Smart Account Native Balance           : %f ETH\n", saETH)
	}

	// 3. Query Factory -> paymaster variable
	fCallData, err := factoryABI.Pack("paymaster")
	if err == nil {
		fResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
			To:   &factoryAddr,
			Data: fCallData,
		}, nil)
		if err == nil {
			var linkedPaymaster common.Address
			factoryABI.UnpackIntoInterface(&linkedPaymaster, "paymaster", fResult)
			fmt.Printf("Factory -> Linked Paymaster            : %s\n", linkedPaymaster.Hex())
		} else {
			fmt.Printf("Factory call failed: %v\n", err)
		}
	}

	// 4. Query Paymaster -> factory variable
	pCallData, err := paymasterABI.Pack("factory")
	if err == nil {
		pResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
			To:   &paymasterAddr,
			Data: pCallData,
		}, nil)
		if err == nil {
			var linkedFactory common.Address
			paymasterABI.UnpackIntoInterface(&linkedFactory, "factory", pResult)
			fmt.Printf("Paymaster -> Linked Factory            : %s\n", linkedFactory.Hex())
		} else {
			fmt.Printf("Paymaster call failed: %v\n", err)
		}
	}

	// 5. Simulate Factory.createAccount(owner, 0) directly
	fmt.Println("\nSimulating factory.createAccount(owner, 0)...")
	createAccountData, err := factoryABI.Pack("createAccount", ownerAddr, big.NewInt(0))
	if err != nil {
		log.Fatalf("Failed to pack createAccount: %v", err)
	}

	msg := ethereum.CallMsg{
		To:   &factoryAddr,
		Data: createAccountData,
	}

	_, err = client.CallContract(context.Background(), msg, nil)
	if err != nil {
		fmt.Printf("❌ Simulation FAILED: %v\n", err)
	} else {
		fmt.Println("✅ Simulation SUCCESSFUL! The factory call did not revert.")
	}
	fmt.Println("===================================================")
}
