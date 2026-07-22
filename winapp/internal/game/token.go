package game

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"encoding/hex"
	"math/big"
	"strings"
)

func generateEncryptedToken(userToken string) string {
	plain := []byte(strings.ToUpper(randomUpperAlnum(8)) + userToken + strings.ToUpper(randomUpperAlnum(8)))
	block, err := aes.NewCipher([]byte("debbde3548928fab"))
	if err != nil {
		return ""
	}
	padded := zeroPad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte("afd4c5c5a7c456a1")).CryptBlocks(out, padded)
	return strings.ToUpper(hex.EncodeToString(out))
}

func zeroPad(data []byte, blockSize int) []byte {
	if len(data)%blockSize == 0 {
		return data
	}
	out := make([]byte, ((len(data)/blockSize)+1)*blockSize)
	copy(out, data)
	return out
}

func randomUpperAlnum(length int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, length)
	for i := range out {
		index, err := crand.Int(crand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			out[i] = alphabet[0]
			continue
		}
		out[i] = alphabet[index.Int64()]
	}
	return string(out)
}
