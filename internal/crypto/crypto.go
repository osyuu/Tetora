// Package crypto provides AES-256-GCM encryption/decryption utilities.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// Encrypt encrypts plaintext using AES-256-GCM with the given key.
// Key is derived via SHA-256 of the key string.
// Nonce (12 bytes) is prepended to ciphertext. Output is hex-encoded.
// Returns plaintext unchanged if key is empty.
func Encrypt(plaintext, key string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if key == "" {
		return plaintext, nil
	}

	keyHash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a hex-encoded AES-256-GCM ciphertext.
// Returns ciphertextHex unchanged if key is empty.
// Gracefully returns the input if decryption fails (likely plaintext data).
func Decrypt(ciphertextHex, key string) (string, error) {
	if ciphertextHex == "" {
		return "", nil
	}
	if key == "" {
		return ciphertextHex, nil
	}

	data, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		// Not hex-encoded — likely plaintext data, return as-is.
		return ciphertextHex, nil
	}

	keyHash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		// Too short for encrypted data — return as-is (plaintext).
		return ciphertextHex, nil
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Decryption failed — likely plaintext data, return as-is.
		return ciphertextHex, nil
	}

	return string(plaintext), nil
}
