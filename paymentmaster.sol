// SPDX-License-Identifier: MIT
pragma solidity ^0.8.12;

import "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import "@openzeppelin/contracts/utils/cryptography/MessageHashUtils.sol";

// ERC-4337 standard UserOperation struct
struct UserOperation {
    address sender;
    uint256 nonce;
    bytes initCode;
    bytes callData;
    uint256 callGasLimit;
    uint256 verificationGasLimit;
    uint256 preVerificationGas;
    uint256 maxFeePerGas;
    uint256 maxPriorityFeePerGas;
    bytes paymasterAndData;
    bytes signature;
}

interface IEntryPoint {
    function depositTo(address account) external payable;
}

interface ICEXPaymaster {
    function addCEXAccount(address account) external;
}

// 1. CEX Smart Account (The User Wallet Contract)
contract CEXSmartAccount {
    using ECDSA for bytes32;

    address public immutable entryPoint;
    address public owner;

    modifier onlyEntryPoint() {
        require(msg.sender == entryPoint, "Only EntryPoint can call");
        _;
    }

    constructor(address _entryPoint, address _owner) {
        entryPoint = _entryPoint;
        owner = _owner;
    }

    // EntryPoint calls this to verify the signature of the UserOperation
    function validateUserOp(
        UserOperation calldata userOp,
        bytes32 userOpHash,
        uint256 missingAccountFunds
    ) external onlyEntryPoint returns (uint256 validationData) {
        // Convert userOpHash to EthSignedMessageHash (standard "\x19Ethereum Signed Message:\n32...")
        bytes32 ethSignedMessageHash = MessageHashUtils.toEthSignedMessageHash(userOpHash);
        
        // Recover signer from signature
        address signer = ethSignedMessageHash.recover(userOp.signature);
        
        // If signer is not the owner, signature validation fails (returns 1)
        if (signer != owner) {
            return 1; // SIG_VALIDATION_FAILED
        }

        // If not using a Paymaster, send missing funds to EntryPoint
        if (missingAccountFunds > 0) {
            (bool success, ) = payable(msg.sender).call{value: missingAccountFunds}("");
            (success);
        }
        
        return 0; // Success
    }

    // Execution function called by EntryPoint to move funds (e.g., sweep USDT)
    function execute(address dest, uint256 value, bytes calldata func) external {
        require(msg.sender == entryPoint || msg.sender == owner, "Only EntryPoint or Owner can execute");
        (bool success, ) = dest.call{value: value}(func);
        require(success, "Execution failed");
    }

    // Accept native ETH deposits
    receive() external payable {}
}

// 2. CEX Smart Account Factory
contract CEXAccountFactory {
    address public immutable entryPoint;
    address public paymaster;
    address public owner;

    modifier onlyOwner() {
        require(msg.sender == owner, "Only owner");
        _;
    }

    constructor(address _entryPoint) {
        entryPoint = _entryPoint;
        owner = msg.sender;
    }

    function setPaymaster(address _paymaster) external onlyOwner {
        paymaster = _paymaster;
    }

    // Deploys the Smart Account deterministically using CREATE2
    function createAccount(address ownerAddress, uint256 salt) public returns (CEXSmartAccount ret) {
        address addr = getAddress(ownerAddress, salt);
        uint codeSize;
        assembly { codeSize := extcodesize(addr) }
        
        // If already deployed, return it
        if (codeSize > 0) {
            return CEXSmartAccount(payable(addr));
        }
        
        // Deploy new contract at the predicted CREATE2 address
        ret = new CEXSmartAccount{salt: bytes32(salt)}(entryPoint, ownerAddress);
        
        // Automatically register in Paymaster whitelist
        if (paymaster != address(0)) {
            ICEXPaymaster(paymaster).addCEXAccount(address(ret));
        }
    }

    // Calculates the counterfactual (predicted) address offline
    function getAddress(address ownerAddress, uint256 salt) public view returns (address) {
        bytes32 hash = keccak256(
            abi.encodePacked(
                bytes1(0xff),
                address(this),
                salt,
                keccak256(abi.encodePacked(
                    type(CEXSmartAccount).creationCode, 
                    abi.encode(entryPoint, ownerAddress)
                ))
            )
        );
        return address(uint160(uint256(hash)));
    }
}

// 3. CEX Paymaster (The Gas Sponsor Contract)
contract CEXPaymaster {
    address public immutable entryPoint;
    address public owner;
    address public factory;
    
    // Whitelist of CEX Smart Accounts that are sponsored
    mapping(address => bool) public isCEXAccount;

    modifier onlyOwner() {
        require(msg.sender == owner, "Only owner");
        _;
    }

    constructor(address _entryPoint) {
        entryPoint = _entryPoint;
        owner = msg.sender;
    }

    function setFactory(address _factory) external onlyOwner {
        factory = _factory;
    }

    // Add a CEX Account to the whitelist (called by Factory or CEX Owner)
    function addCEXAccount(address account) external {
        require(msg.sender == factory || msg.sender == owner, "Unauthorized to whitelist");
        isCEXAccount[account] = true;
    }

    // Deposit ETH directly to the EntryPoint to fund gas sponsorship
    function deposit() public payable {
        // EntryPoint v0.6 uses depositTo to lock ETH for paymaster
        (bool success, ) = entryPoint.call{value: msg.value}(
            abi.encodeWithSignature("depositTo(address)", address(this))
        );
        require(success, "Deposit to EntryPoint failed");
    }

    // EntryPoint calls this to verify if the Paymaster sponsors this operation
    function validatePaymasterUserOp(
        UserOperation calldata userOp,
        bytes32 /* userOpHash */,
        uint256 /* maxCost */
    ) external view returns (bytes memory context, uint256 validationData) {
        require(msg.sender == entryPoint, "Only EntryPoint can call");

        // SECURITY CHECK:
        // We only sponsor if the sender address is a registered CEX Smart Account
        if (!isCEXAccount[userOp.sender]) {
            return ("", 1); // Reject validation (1 = signature/validation failed)
        }

        return ("", 0); // Accept validation, sponsor transaction
    }

    // EntryPoint calls this after execution (required by standard interface)
    function postOp(
        uint8 /* mode */,
        bytes calldata /* context */,
        uint256 /* actualGasCost */
    ) external {
        // No extra logic needed post-execution
    }
}
