package ontology

import (
	"fmt"
	"testing"
)

// findKey scans deterministic probe strings until one whose hash satisfies
// pred. The result is fixed because the hash is fixed.
func findKey(t *testing.T, pred func(uint64) bool) (string, uint64) {
	t.Helper()
	for i := 0; ; i++ {
		k := fmt.Sprintf("wrap/probe/%d", i)
		h := hashString(k)
		if pred(h) {
			return k, h
		}
		if i > 5_000_000 {
			t.Fatal("no probe key satisfied the range predicate")
		}
	}
}

func TestWrapAround(t *testing.T) {
	const (
		loPos = uint64(1) << 62
		hiPos = loPos + 1<<40
	)
	ring := newHandcraftedRing([]vnode{
		{pos: loPos, node: "lo-node"},
		{pos: hiPos, node: "hi-node"},
	})

	beforeKey, beforeHash := findKey(t, func(h uint64) bool { return h < loPos })
	betweenKey, betweenHash := findKey(t, func(h uint64) bool {
		return h > loPos && h < hiPos
	})
	afterKey, afterHash := findKey(t, func(h uint64) bool { return h > hiPos })

	cases := []struct {
		name string
		key  string
		hash uint64
		want string
	}{
		{"before minimum wraps", beforeKey, beforeHash, "lo-node"},
		{"between vnodes", betweenKey, betweenHash, "hi-node"},
		{"after maximum wraps", afterKey, afterHash, "lo-node"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ring.Locate(c.key)
			if err != nil {
				t.Fatalf("Locate: %v", err)
			}
			if got != c.want {
				t.Fatalf("hash %#x: got %q, want %q", c.hash, got, c.want)
			}
		})
	}
}

// A key hashing exactly onto a vnode position belongs to that vnode.
func TestExactVnodePosition(t *testing.T) {
	key := "exact/anchor/key"
	pos := hashString(key)
	ring := newHandcraftedRing([]vnode{
		{pos: pos, node: "exact"},
	})
	got, err := ring.Locate(key)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got != "exact" {
		t.Fatalf("exact hit resolved to %q, want exact", got)
	}
}
