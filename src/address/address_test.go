package address

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestAddress_Address_IsValid(t *testing.T) {
	prefix := GetPrefix()
	var address Address
	_, _ = rand.Read(address[:])

	// Wrong first byte — definitely not our prefix.
	address[0] = ^prefix[0]
	if address.IsValid() {
		t.Fatal("invalid address marked as valid")
	}

	// Subnet prefix byte (low bit set) — valid subnet, invalid address.
	address[0] = prefix[0] | 0x01
	if address.IsValid() {
		t.Fatal("subnet prefix byte should not be a valid address")
	}

	// Correct node prefix byte.
	address[0] = prefix[0]
	if !address.IsValid() {
		t.Fatal("valid address marked as invalid")
	}
}

func TestAddress_Subnet_IsValid(t *testing.T) {
	prefix := GetPrefix()
	var subnet Subnet
	_, _ = rand.Read(subnet[:])

	// Wrong first byte.
	subnet[0] = ^prefix[0]
	if subnet.IsValid() {
		t.Fatal("invalid subnet marked as valid")
	}

	// Node prefix byte (low bit clear) — valid address, invalid subnet.
	subnet[0] = prefix[0]
	if subnet.IsValid() {
		t.Fatal("node prefix byte should not be a valid subnet")
	}

	// Correct subnet prefix byte (low bit set).
	subnet[0] = prefix[0] | 0x01
	if !subnet.IsValid() {
		t.Fatal("valid subnet marked as invalid")
	}
}

func TestAddress_AddrForKey(t *testing.T) {
	publicKey := ed25519.PublicKey{
		189, 186, 207, 216, 34, 64, 222, 61, 205, 18, 57, 36, 203, 181, 82, 86,
		251, 141, 171, 8, 170, 152, 227, 5, 82, 138, 184, 79, 65, 158, 110, 251,
	}

	expectedAddress := Address{
		2, 0, 132, 138, 96, 79, 187, 126, 67, 132, 101, 219, 141, 182, 104, 149,
	}

	if *AddrForKey(publicKey) != expectedAddress {
		t.Fatal("invalid address returned")
	}
}

func TestAddress_SubnetForKey(t *testing.T) {
	publicKey := ed25519.PublicKey{
		189, 186, 207, 216, 34, 64, 222, 61, 205, 18, 57, 36, 203, 181, 82, 86,
		251, 141, 171, 8, 170, 152, 227, 5, 82, 138, 184, 79, 65, 158, 110, 251,
	}

	expectedSubnet := Subnet{3, 0, 132, 138, 96, 79, 187, 126}

	if *SubnetForKey(publicKey) != expectedSubnet {
		t.Fatal("invalid subnet returned")
	}
}

func TestAddress_Address_GetKey(t *testing.T) {
	address := Address{
		2, 0, 132, 138, 96, 79, 187, 126, 67, 132, 101, 219, 141, 182, 104, 149,
	}

	expectedPublicKey := ed25519.PublicKey{
		189, 186, 207, 216, 34, 64, 222, 61,
		205, 18, 57, 36, 203, 181, 127, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
	}

	if !bytes.Equal(address.GetKey(), expectedPublicKey) {
		t.Fatal("invalid public key returned")
	}
}

func TestAddress_Subnet_GetKey(t *testing.T) {
	subnet := Subnet{3, 0, 132, 138, 96, 79, 187, 126}

	expectedPublicKey := ed25519.PublicKey{
		189, 186, 207, 216, 34, 64, 255, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
	}

	if !bytes.Equal(subnet.GetKey(), expectedPublicKey) {
		t.Fatal("invalid public key returned")
	}
}

func TestSetPrefix(t *testing.T) {
	original := GetPrefix()
	defer func() { globalPrefix = original }()

	// Odd prefix must be rejected.
	if err := SetPrefix(0x03); err == nil {
		t.Fatal("expected error for odd prefix byte")
	}

	// Even prefix must be accepted.
	if err := SetPrefix(0x04); err != nil {
		t.Fatalf("unexpected error for even prefix byte: %v", err)
	}
	if GetPrefix() != [1]byte{0x04} {
		t.Fatal("prefix not updated")
	}

	// Addresses and subnets should reflect the new prefix.
	pub, _, _ := ed25519.GenerateKey(nil)
	addr := AddrForKey(pub)
	if addr[0] != 0x04 {
		t.Fatalf("address first byte: want 0x04, got 0x%02x", addr[0])
	}
	snet := SubnetForKey(pub)
	if snet[0] != 0x05 {
		t.Fatalf("subnet first byte: want 0x05, got 0x%02x", snet[0])
	}
}
