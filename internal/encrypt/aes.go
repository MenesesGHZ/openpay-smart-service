// Package encrypt provides AES-256-GCM helpers for encrypting sensitive values
// at rest (tenant CLABE numbers, webhook subscription secrets).
//
// Ciphertext format (base64-encoded):
//
//	[ 12-byte nonce | N-byte ciphertext | 16-byte GCM auth tag ]
//
// The nonce is randomly generated for each encryption; no nonce is ever reused.
package encrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

// Encrypt encrypts plaintext with AES-256-GCM using keyHex (64 hex chars = 32 bytes).
// Returns a base64-encoded ciphertext that includes the random nonce prefix.
func Encrypt(keyHex, plaintext string) (string, error) {
	key, err := decodeKey(keyHex)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("aes gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// Seal appends ciphertext+tag to nonce.
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded ciphertext produced by Encrypt.
func Decrypt(keyHex, encoded string) (string, error) {
	key, err := decodeKey(keyHex)
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("aes gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("aes gcm open: %w", err)
	}

	return string(plaintext), nil
}

// MaskCLABE returns a masked version of an 18-digit CLABE showing only the last 4.
// e.g. "032180000118359719" → "**************9719"
func MaskCLABE(clabe string) string {
	if len(clabe) <= 4 {
		return clabe
	}
	masked := make([]byte, len(clabe))
	for i := range masked {
		if i < len(clabe)-4 {
			masked[i] = '*'
		} else {
			masked[i] = clabe[i]
		}
	}
	return string(masked)
}

func decodeKey(keyHex string) ([]byte, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("decode AES key hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("AES key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}
	return key, nil
}
