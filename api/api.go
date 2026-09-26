// Package api is the outward-facing Base64 facade. Dependency direction is
// strictly one way: api -> codec -> b64; nothing here is imported by them.
package api

import (
	"encoding/base64"
	"errors"
	"fmt"

	"ontology/b64"
	"ontology/codec"
)

// API is stateless; its methods are safe for concurrent use.
type API struct{}

// New returns the facade.
func New() *API { return &API{} }

// EncodeString encodes a UTF-8/byte string into padded standard Base64.
func (a *API) EncodeString(s string) string {
	return string(b64.Encode([]byte(s)))
}

// DecodeString validates strictly and returns the decoded string. Any
// rejection yields ("", err) with no partial result.
func (a *API) DecodeString(s string) (string, error) {
	out, err := b64.Decode([]byte(s))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SelfCheck runs the four invariants over built-in vectors and returns nil
// only if every one holds. It is deterministic and uses no randomness.
func (a *API) SelfCheck() error {
	// Invariants 1-3 over lengths 0..9 and a long buffer: round trip plus
	// byte-identity with both the stdlib reference and fixed literals.
	for _, n := range append(lengths(), 256) {
		raw := make([]byte, n)
		for i := range raw {
			raw[i] = byte(i % 256)
		}
		enc := a.EncodeString(string(raw))
		if enc != base64.StdEncoding.EncodeToString(raw) {
			return fmt.Errorf("canonical mismatch at n=%d", n)
		}
		got, err := a.DecodeString(enc)
		if err != nil || got != string(raw) {
			return fmt.Errorf("roundtrip failure at n=%d: %v", n, err)
		}
	}
	lit := []struct{ raw, want string }{
		{"", ""}, {"f", "Zg=="}, {"fo", "Zm8="},
		{"foo", "Zm9v"}, {"foobarb", "Zm9vYmFyYg=="},
	}
	for _, c := range lit {
		if got := a.EncodeString(c.raw); got != c.want {
			return fmt.Errorf("literal %q: got %q want %q", c.raw, got, c.want)
		}
	}
	// Invariant 4: the four rejection classes are distinct sentinels.
	bad := []struct {
		in   string
		want error
	}{
		{"Zm9*", b64.ErrChar},
		{"abc", b64.ErrLength},
		{"Z===", b64.ErrPadding},
		{"Zm9=", b64.ErrUnusedBits},
	}
	for _, c := range bad {
		if _, err := a.DecodeString(c.in); !errors.Is(err, c.want) {
			return fmt.Errorf("input %q: got %v want %v", c.in, err, c.want)
		}
	}
	if _, err := a.DecodeString("Zm9vYmFyYg=="); err != nil { // still usable
		return fmt.Errorf("not reusable after rejected decode: %w", err)
	}
	// codec: direct block access and out-of-range rejection.
	buf := a.EncodeString("foobarbaz") // 3 blocks
	if codec.BlockCount([]byte(buf)) != 3 {
		return errors.New("BlockCount mismatch")
	}
	blk, err := codec.DecodeBlockAt([]byte(buf), 1)
	if err != nil || string(blk) != "bar" {
		return fmt.Errorf("DecodeBlockAt middle block: %q %v", blk, err)
	}
	if _, err := codec.DecodeBlockAt([]byte(buf), 3); !errors.Is(err, codec.ErrBlockOutOfRange) {
		return fmt.Errorf("expected out-of-range, got %v", err)
	}
	return nil
}

func lengths() []int {
	s := make([]int, 10) // 0..9
	for i := range s {
		s[i] = i
	}
	return s
}
