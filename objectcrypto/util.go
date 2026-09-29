package objectcrypto

import (
	"crypto/rand"
	"encoding/base64"
)

func readRandom(b []byte) (int, error) {
	return rand.Read(b)
}

func base64Std(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
