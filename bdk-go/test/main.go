package main

import (
	"fmt"
	"log"

	bdk "github.com/sbn20241/bdk-ffi/bdk-go/bdk"
)

func main() {
	fmt.Println("Testing BDK Go Bindings...")

	// Test 1: Create an Amount
	fmt.Println("\n1. Testing Amount:")
	amount := bdk.AmountFromSat(1000000) // 0.01 BTC
	fmt.Printf("   Amount in sat: %d\n", amount.ToSat())
	fmt.Printf("   Amount in BTC: %.8f\n", amount.ToBtc())
	defer amount.Destroy()

	// Test 2: Create a Mnemonic
	fmt.Println("\n2. Testing Mnemonic:")
	mnemonic := bdk.NewMnemonic(bdk.WordCountWords12)
	fmt.Printf("   Mnemonic created successfully\n")
	fmt.Printf("   Mnemonic string: %s\n", mnemonic.String())
	defer mnemonic.Destroy()

	// Test 3: Create Mnemonic from string
	fmt.Println("\n3. Testing Mnemonic from string:")
	testMnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	mnemonic2, err := bdk.MnemonicFromString(testMnemonic)
	if err != nil {
		log.Fatalf("Failed to create mnemonic from string: %v", err)
	}
	fmt.Printf("   Mnemonic from string: %s\n", mnemonic2.String())
	defer mnemonic2.Destroy()

	// Test 4: Create an Address
	fmt.Println("\n4. Testing Address:")
	addressStr := "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx"
	address, err := bdk.NewAddress(addressStr, bdk.NetworkTestnet)
	if err != nil {
		log.Fatalf("Failed to create address: %v", err)
	}
	fmt.Printf("   Address created: %s\n", address.String())
	fmt.Printf("   Is valid for testnet: %v\n", address.IsValidForNetwork(bdk.NetworkTestnet))
	defer address.Destroy()

	// Test 5: Create FeeRate
	fmt.Println("\n5. Testing FeeRate:")
	feeRate, err := bdk.FeeRateFromSatPerVb(10) // 10 sat/vb
	if err != nil {
		log.Fatalf("Failed to create fee rate: %v", err)
	}
	fmt.Printf("   Fee rate sat/vb (ceil): %d\n", feeRate.ToSatPerVbCeil())
	fmt.Printf("   Fee rate sat/kwu: %d\n", feeRate.ToSatPerKwu())
	defer feeRate.Destroy()

	fmt.Println("\n✅ All basic tests passed!")

	// Test 6: Create a wallet that can sign transactions
	fmt.Println("\n6. Testing Wallet Creation and Signing:")

	// Create a mnemonic for the wallet
	walletMnemonic := bdk.NewMnemonic(bdk.WordCountWords12)
	fmt.Printf("   Wallet mnemonic: %s\n", walletMnemonic.String())
	defer walletMnemonic.Destroy()

	// Create a descriptor secret key from the mnemonic
	network := bdk.NetworkTestnet
	secretKey := bdk.NewDescriptorSecretKey(network, walletMnemonic, nil)
	defer secretKey.Destroy()

	// Create external descriptor (BIP84 - native segwit)
	externalDescriptor := bdk.DescriptorNewBip84(secretKey, bdk.KeychainKindExternal, network)
	defer externalDescriptor.Destroy()
	fmt.Printf("   External descriptor created\n")

	// Create change descriptor (internal keychain)
	changeDescriptor := bdk.DescriptorNewBip84(secretKey, bdk.KeychainKindInternal, network)
	defer changeDescriptor.Destroy()
	fmt.Printf("   Change descriptor created\n")

	// Create an in-memory database connection
	connection, err := bdk.ConnectionNewInMemory()
	if err != nil {
		log.Fatalf("Failed to create connection: %v", err)
	}
	defer connection.Destroy()
	fmt.Printf("   Database connection created\n")

	// Create the wallet
	wallet, err := bdk.NewWallet(externalDescriptor, changeDescriptor, network, connection)
	if err != nil {
		log.Fatalf("Failed to create wallet: %v", err)
	}
	defer wallet.Destroy()
	fmt.Printf("   Wallet created successfully\n")

	// Get wallet balance (should be zero for a new wallet)
	balance := wallet.Balance()
	fmt.Printf("   Wallet balance: %d sats (confirmed: %d, trusted pending: %d, untrusted pending: %d)\n",
		balance.Total.ToSat(),
		balance.Confirmed.ToSat(),
		balance.TrustedPending.ToSat(),
		balance.UntrustedPending.ToSat())
	defer balance.Destroy()

	// Create a transaction builder
	txBuilder := bdk.NewTxBuilder()
	defer txBuilder.Destroy()
	fmt.Printf("   Transaction builder created\n")

	// Example: Create a recipient address (you would use a real address in production)
	recipientAddress, err := bdk.NewAddress("tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", network)
	if err != nil {
		log.Fatalf("Failed to create recipient address: %v", err)
	}
	defer recipientAddress.Destroy()

	// Get the script from the address
	recipientScript := recipientAddress.ScriptPubkey()
	defer recipientScript.Destroy()

	// Add a recipient (example: sending 1000 sats)
	// Note: This will fail if the wallet has no UTXOs, but demonstrates the API
	sendAmount := bdk.AmountFromSat(1000)
	defer sendAmount.Destroy()
	txBuilder = txBuilder.AddRecipient(recipientScript, sendAmount)
	fmt.Printf("   Added recipient: 1000 sats\n")

	// Set a fee rate
	feeRate, err = bdk.FeeRateFromSatPerVb(5) // 5 sat/vb
	if err != nil {
		log.Fatalf("Failed to create fee rate: %v", err)
	}
	defer feeRate.Destroy()
	txBuilder = txBuilder.FeeRate(feeRate)
	fmt.Printf("   Set fee rate: 5 sat/vb\n")

	// Build the PSBT (Partially Signed Bitcoin Transaction)
	// Note: This will fail if wallet has no UTXOs, but shows the API
	psbt, err := txBuilder.Finish(wallet)
	if err != nil {
		fmt.Printf("   ⚠️  Could not build PSBT (expected for empty wallet): %v\n", err)
		fmt.Printf("   This is normal - the wallet needs UTXOs to build a transaction\n")
	} else {
		defer psbt.Destroy()
		fmt.Printf("   PSBT created successfully\n")

		// Sign the PSBT
		signOptions := &bdk.SignOptions{
			TrustWitnessUtxo:       false,
			AllowAllSighashes:      false,
			TryFinalize:            true,
			SignWithTapInternalKey: true,
			AllowGrinding:          true,
		}
		finalized, err := wallet.Sign(psbt, signOptions)
		if err != nil {
			log.Fatalf("Failed to sign PSBT: %v", err)
		}
		if finalized {
			fmt.Printf("   ✅ PSBT signed and finalized successfully!\n")
		} else {
			fmt.Printf("   ✅ PSBT signed but not finalized (may need additional signers)\n")
		}

		// You can also finalize separately if needed
		finalized2, err := wallet.FinalizePsbt(psbt, signOptions)
		if err != nil {
			fmt.Printf("   ⚠️  Finalization error (may already be finalized): %v\n", err)
		} else if finalized2 {
			fmt.Printf("   ✅ PSBT finalized\n")
		}
	}

	fmt.Println("\n✅ Wallet signing test completed!")
}
