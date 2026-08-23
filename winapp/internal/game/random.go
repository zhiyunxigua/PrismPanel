package game

import (
	crand "crypto/rand"
	"encoding/hex"
)

// randomHex 返回 size 字节随机数的十六进制字符串（国际版认证/队列共用）。
func randomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := crand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
