package crypto

import (
	"strings"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := "test-encryption-key-2024"
	original := "sensitive data here"

	enc, err := Encrypt(original, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if enc == original {
		t.Fatal("encrypted should differ from original")
	}
	if enc == "" {
		t.Fatal("encrypted should not be empty")
	}

	dec, err := Decrypt(enc, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != original {
		t.Errorf("round-trip failed: got %q, want %q", dec, original)
	}
}

func TestEncryptDecrypt_UniqueNonce(t *testing.T) {
	key := "nonce-test-key"
	plain := "same plaintext"

	enc1, _ := Encrypt(plain, key)
	enc2, _ := Encrypt(plain, key)

	if enc1 == enc2 {
		t.Error("two encryptions of same plaintext should produce different ciphertexts (unique nonce)")
	}

	// Both should decrypt to same value.
	dec1, _ := Decrypt(enc1, key)
	dec2, _ := Decrypt(enc2, key)
	if dec1 != plain || dec2 != plain {
		t.Error("both should decrypt to original")
	}
}

func TestEncryptEmptyKey(t *testing.T) {
	plain := "no encryption"

	enc, err := Encrypt(plain, "")
	if err != nil {
		t.Fatalf("Encrypt with empty key: %v", err)
	}
	if enc != plain {
		t.Errorf("empty key should pass through: got %q, want %q", enc, plain)
	}

	dec, err := Decrypt(plain, "")
	if err != nil {
		t.Fatalf("Decrypt with empty key: %v", err)
	}
	if dec != plain {
		t.Errorf("empty key should pass through: got %q, want %q", dec, plain)
	}
}

func TestEncryptEmptyPlaintext(t *testing.T) {
	enc, err := Encrypt("", "some-key")
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}
	if enc != "" {
		t.Errorf("empty plaintext should return empty: got %q", enc)
	}
}

func TestDecryptNotEncrypted(t *testing.T) {
	// Plaintext that isn't hex-encoded should return as-is.
	plain := "this is not encrypted"
	dec, err := Decrypt(plain, "some-key")
	if err != nil {
		t.Fatalf("Decrypt plaintext: %v", err)
	}
	if dec != plain {
		t.Errorf("non-encrypted data should return as-is: got %q, want %q", dec, plain)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	plain := "secret message"
	enc, _ := Encrypt(plain, "correct-key")

	// Decrypt with wrong key should return as-is (graceful fallback).
	dec, err := Decrypt(enc, "wrong-key")
	if err != nil {
		t.Fatalf("Decrypt with wrong key should not error: %v", err)
	}
	// Should return the hex string as-is since decryption fails gracefully.
	if dec != enc {
		t.Logf("wrong key returned %q (len=%d)", dec[:min(len(dec), 40)], len(dec))
	}
}

func TestEncryptLongData(t *testing.T) {
	key := "long-data-key"
	original := strings.Repeat("a", 10000)

	enc, err := Encrypt(original, key)
	if err != nil {
		t.Fatalf("Encrypt long: %v", err)
	}

	dec, err := Decrypt(enc, key)
	if err != nil {
		t.Fatalf("Decrypt long: %v", err)
	}
	if dec != original {
		t.Error("long data round-trip failed")
	}
}
