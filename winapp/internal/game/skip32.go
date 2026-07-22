package game

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"strconv"
)

var skip32FTable = []byte{
	163, 215, 9, 131, 248, 72, 246, 244, 179, 33,
	21, 120, 153, 177, 175, 249, 231, 45, 77, 138,
	206, 76, 202, 46, 82, 149, 217, 30, 78, 56,
	68, 40, 10, 223, 2, 160, 23, 241, 96, 104,
	18, 183, 122, 195, 233, 250, 61, 83, 150, 132,
	107, 186, 242, 99, 154, 25, 124, 174, 229, 245,
	247, 22, 106, 162, 57, 182, 123, 15, 193, 147,
	129, 27, 238, 180, 26, 234, 208, 145, 47, 184,
	85, 185, 218, 133, 63, 65, 191, 224, 90, 88,
	128, 95, 102, 11, 216, 144, 53, 213, 192, 167,
	51, 6, 101, 105, 69, 0, 148, 86, 109, 152,
	155, 118, 151, 252, 178, 194, 176, 254, 219, 32,
	225, 235, 214, 228, 221, 71, 74, 29, 66, 237,
	158, 110, 73, 60, 205, 67, 39, 210, 7, 212,
	222, 199, 103, 24, 137, 203, 48, 31, 141, 198,
	143, 170, 200, 116, 220, 201, 93, 92, 49, 164,
	112, 136, 97, 44, 159, 13, 43, 135, 80, 130,
	84, 100, 38, 125, 3, 64, 52, 75, 28, 115,
	209, 196, 253, 59, 204, 251, 127, 171, 230, 62,
	91, 165, 173, 4, 35, 156, 20, 81, 34, 240,
	41, 121, 113, 126, 255, 140, 14, 226, 12, 239,
	188, 114, 117, 111, 55, 161, 236, 211, 142, 98,
	139, 134, 16, 232, 8, 119, 17, 190, 146, 79,
	36, 197, 50, 54, 157, 207, 243, 166, 187, 172,
	94, 108, 169, 19, 87, 37, 181, 227, 189, 168,
	58, 1, 5, 89, 42, 70,
}

func generateRoleUUID(roleName, userID string) string {
	parsed, _ := strconv.ParseUint(userID, 10, 32)
	hash := md5.Sum([]byte(roleName))
	encrypted := skip32Encrypt(uint32(parsed), []byte("SaintSteve"))
	binary.LittleEndian.PutUint32(hash[12:], encrypted)
	hash[6] = (hash[6] & 0x0f) | 0x40
	hash[8] = (hash[8] & 0x3f) | 0x80
	return hex.EncodeToString(hash[:])
}

func skip32Encrypt(value uint32, key []byte) uint32 {
	buf := []int{int(value >> 24 & 0xff), int(value >> 16 & 0xff), int(value >> 8 & 0xff), int(value & 0xff)}
	skip32(key, buf, true)
	return uint32(buf[0]<<24 | buf[1]<<16 | buf[2]<<8 | buf[3])
}

func skip32(key []byte, buf []int, encrypt bool) {
	step := 1
	k := 0
	if !encrypt {
		step = -1
		k = 23
	}
	w1 := (buf[0] << 8) + buf[1]
	w2 := (buf[2] << 8) + buf[3]
	for i := 0; i < 12; i++ {
		w2 ^= skip32G(key, k, w1) ^ k
		k2 := k + step
		w1 ^= skip32G(key, k2, w2) ^ k2
		k = k2 + step
	}
	buf[0] = w2 >> 8
	buf[1] = w2 & 0xff
	buf[2] = w1 >> 8
	buf[3] = w1 & 0xff
}

func skip32G(key []byte, k, w int) int {
	num := w >> 8
	num2 := w & 0xff
	num3 := int(skip32FTable[num2^int(key[(4*k)%10]&0xff)]) ^ num
	num4 := int(skip32FTable[num3^int(key[(4*k+1)%10]&0xff)]) ^ num2
	num5 := int(skip32FTable[num4^int(key[(4*k+2)%10]&0xff)]) ^ num3
	num6 := int(skip32FTable[num5^int(key[(4*k+3)%10]&0xff)]) ^ num4
	return (num5 << 8) + num6
}
