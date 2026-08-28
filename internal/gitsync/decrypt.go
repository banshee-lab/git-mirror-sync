package gitsync

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
)

// DecryptForgejoSecret decrypts a bytea field from the Forgejo database.
// 'secretKey' should be your instance's internal secret key (or derived 32-byte key/hash).
// 'ciphertext' is the raw []byte scanned directly from the database column.
func DecryptForgejoSecret(secretKeyStr string, ciphertext []byte) ([]byte, error) {
  if len(ciphertext) == 0 {
    return nil, nil
  }

  // Forgejo derives or uses a key length compatible with AES (typically hashing/padding the SECRET_KEY to 16, 24, or 32 bytes)
  // Ensure your key matches the required AES block size (e.g., 32 bytes for AES-256 via SHA-256 if needed, 
  // or use the raw bytes if your key is already correctly sized).
  key := []byte(secretKeyStr)
  if len(key) < 32 {
    // Pad or hash to fit 32 bytes if your configuration string is shorter
    // (Alternatively, Forgejo uses modules/secret package logic)
    needed := 32 - len(key)
    key = append(key, make([]byte, needed)...)
  } else if len(key) > 32 {
    key = key[:32]
  }

  block, err := aes.NewCipher(key)
  if err != nil {
    return nil, fmt.Errorf("failed to create cipher: %w", err)
  }

  if len(ciphertext) < aes.BlockSize {
    return nil, errors.New("ciphertext too short")
  }

  // Extract IV from the beginning of the ciphertext block
  iv := ciphertext[:aes.BlockSize]
  encryptedData := ciphertext[aes.BlockSize:]

  // Perform CFB decryption
  cfb := cipher.NewCFBDecrypter(block, iv)
  cfb.XORKeyStream(encryptedData, encryptedData)

  // Forgejo base64-encodes the underlying string before encrypting it
  decoded, err := base64.StdEncoding.DecodeString(string(encryptedData))
  if err != nil {
    // Fallback if it wasn't base64 encoded in older instances
    return encryptedData, nil
  }

  return decoded, nil
}