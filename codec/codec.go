// Package codec is the public string-level front end of the base32 codec.
// It depends only on the b32 package.
package codec

import (
	"bytes"
	"fmt"
	"strings"

	"ontology/b32"
)

// shared is the process-internal codec behind the package-level functions.
var shared = b32.New()

// EncodeString returns the base32 encoding of s.
func EncodeString(s string) string {
	return shared.Encode([]byte(s))
}

// DecodeString decodes a base32 string. Rejections return a sentinel error
// from b32 and no partial result.
func DecodeString(s string) (string, error) {
	b, err := shared.Decode(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SelfCheck round-trips inputs of length 0..20 and verifies for each that
// the encoded length is 8*ceil(n/5) and the padding count is legal.
func SelfCheck() error {
	for n := 0; n <= 20; n++ {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(i*37 + n)
		}
		enc := EncodeString(string(src))
		if len(enc) != 8*((n+4)/5) {
			return fmt.Errorf("selfcheck: n=%d encoded length %d", n, len(enc))
		}
		pad := len(enc) - len(strings.TrimRight(enc, "="))
		switch pad {
		case 0, 1, 3, 4, 6:
		default:
			return fmt.Errorf("selfcheck: n=%d illegal padding %d", n, pad)
		}
		dec, err := DecodeString(enc)
		if err != nil {
			return fmt.Errorf("selfcheck: n=%d decode: %w", n, err)
		}
		if !bytes.Equal([]byte(dec), src) {
			return fmt.Errorf("selfcheck: n=%d round trip mismatch", n)
		}
	}
	return nil
}
