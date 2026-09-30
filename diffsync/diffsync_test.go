package diffsync

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func randomBytes(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	r.Read(b)
	return b
}

func shortHash(b []byte) string {
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h[:4])
}

// roundTrip builds a signature from old, generates a delta to new, and
// applies it, returning the patch, stats and rebuilt bytes.
func roundTrip(t *testing.T, old, newData []byte, blockSize int) ([]byte, Stats, []byte) {
	sig, err := BuildSignature(old, blockSize)
	if err != nil {
		t.Fatalf("BuildSignature: %v", err)
	}
	patch, st, err := GenerateDelta(sig, newData)
	if err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	if st.StrongChecks > st.WeakHits {
		t.Fatalf("invariant violated: StrongChecks=%d > WeakHits=%d", st.StrongChecks, st.WeakHits)
	}
	out, err := Apply(old, patch)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(out, newData) {
		t.Fatalf("round trip mismatch: got len=%d sha=%s, want len=%d sha=%s",
			len(out), shortHash(out), len(newData), shortHash(newData))
	}
	t.Logf("input old=%dB(sha=%s) new=%dB(sha=%s) bs=%d -> patch=%dB, "+
		"stats=%+v; output matches new file byte-for-byte",
		len(old), shortHash(old), len(newData), shortHash(newData),
		blockSize, len(patch), st)
	return patch, st, out
}

func literalBytesInPatch(t *testing.T, patch []byte) int {
	p, err := UnmarshalPatch(patch)
	if err != nil {
		t.Fatalf("UnmarshalPatch: %v", err)
	}
	n := 0
	for _, in := range p.Insns {
		if in.Op == OpLiteral {
			n += len(in.Data)
		}
	}
	return n
}

func TestRoundTripScenarios(t *testing.T) {
	const bs = 64
	base := randomBytes(bs*10+17, 1) // not a multiple of bs: exercises the short last block

	modified := bytes.Clone(base)
	copy(modified[3*bs+10:], []byte{0xDE, 0xAD, 0xBE, 0xEF}) // in-place edit inside block 3

	inserted := bytes.Clone(base)
	inserted = append(inserted[:2*bs], append(randomBytes(30, 2), inserted[2*bs:]...)...)

	deleted := append(bytes.Clone(base[:bs]), base[3*bs:]...)

	cases := []struct {
		name string
		old  []byte
		new  []byte
	}{
		{"identical", base, bytes.Clone(base)},
		{"in-place modify", base, modified},
		{"insert middle", base, inserted},
		{"delete middle", base, deleted},
		{"append", base, append(bytes.Clone(base), randomBytes(40, 3)...)},
		{"prepend", base, append(randomBytes(40, 4), base...)},
		{"empty old", nil, randomBytes(100, 5)},
		{"empty new", base, nil},
		{"both empty", nil, nil},
		{"old shorter than block", randomBytes(10, 6), randomBytes(10, 7)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("input: old=%dB(sha=%s) new=%dB(sha=%s)",
				len(tc.old), shortHash(tc.old), len(tc.new), shortHash(tc.new))
			patch, _, _ := roundTrip(t, tc.old, tc.new, bs)
			if tc.name == "identical" {
				if n := literalBytesInPatch(t, patch); n != 0 {
					t.Fatalf("identical files: patch contains %d literal bytes, want 0", n)
				}
				t.Logf("decision: identical input -> patch has no literal bytes, as required")
			}
			if tc.name == "in-place modify" {
				if n := literalBytesInPatch(t, patch); n > bs {
					t.Fatalf("in-place modify: %d literal bytes > one block (%d)", n, bs)
				}
				t.Logf("decision: in-place modify -> %d literal bytes <= block size %d",
					literalBytesInPatch(t, patch), bs)
			}
		})
	}
}

// TestWeakCollisionNotReused builds two blocks with equal weak checksums
// but different content and asserts the impostor is never reused.
func TestWeakCollisionNotReused(t *testing.T) {
	const bs = 2
	old := []byte{0x0A, 0x14, 0x01, 0x02}     // block0 sum=30, block1 sum=3
	newData := []byte{0x14, 0x0A, 0x01, 0x02} // impostor {0x14,0x0A} sum=30 != block0 content

	sig, err := BuildSignature(old, bs)
	if err != nil {
		t.Fatalf("BuildSignature: %v", err)
	}
	impostorStrong := strongSum(newData[0:2])
	t.Logf("input: old=%x new=%x; weak(block0)=%d weak(new[0:2])=%d collide, "+
		"strong(block0)=%x strong(new[0:2])=%x differ",
		old, newData, weakSum(old[0:2]), weakSum(newData[0:2]),
		sig.Blocks[0].Strong[:4], impostorStrong[:4])

	patch, st, err := GenerateDelta(sig, newData)
	if err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	p, err := UnmarshalPatch(patch)
	if err != nil {
		t.Fatalf("UnmarshalPatch: %v", err)
	}
	for _, in := range p.Insns {
		if in.Op == OpCopy && in.Index == 0 {
			t.Fatalf("impostor block reused: patch copies block 0 despite strong mismatch")
		}
	}
	if st.WeakHits == 0 || st.StrongChecks == 0 {
		t.Fatalf("expected the weak collision to trigger a strong check, stats=%+v", st)
	}
	out, err := Apply(old, patch)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(out, newData) {
		t.Fatalf("output %x != new %x", out, newData)
	}
	t.Logf("decision: weak hit rejected by strong checksum -> bytes emitted literally; "+
		"stats=%+v; output=%x matches new file", st, out)
}

// TestShortLastBlockLengthRule: the short last block must only match an
// equally short fragment, never a full-size window with the same weak sum.
func TestShortLastBlockLengthRule(t *testing.T) {
	const bs = 4
	old := []byte{9, 9, 9, 9, 5, 5} // block0 weak=36; last block {5,5}: len=2, weak=10
	newData := []byte{1, 2, 3, 4}   // full window weak=10 collides only with the short last block

	sig, err := BuildSignature(old, bs)
	if err != nil {
		t.Fatalf("BuildSignature: %v", err)
	}
	t.Logf("input: old=%x new=%x; last block len=%d weak=%d, new window len=%d weak=%d",
		old, newData, sig.Blocks[1].Len, sig.Blocks[1].Weak, bs, weakSum(newData))

	patch, st, err := GenerateDelta(sig, newData)
	if err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	p, err := UnmarshalPatch(patch)
	if err != nil {
		t.Fatalf("UnmarshalPatch: %v", err)
	}
	for _, in := range p.Insns {
		if in.Op == OpCopy && in.Index == 1 {
			t.Fatalf("short last block reused for a full-size window")
		}
	}
	if st.StrongChecks != 0 {
		t.Fatalf("length mismatch must be rejected before the strong check, stats=%+v", st)
	}
	out, err := Apply(old, patch)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(out, newData) {
		t.Fatalf("output %x != new %x", out, newData)
	}
	t.Logf("decision: length mismatch filtered before strong check (StrongChecks=%d); "+
		"output=%x matches new file", st.StrongChecks, out)
}

func TestDeterministicPatch(t *testing.T) {
	const bs = 32
	old := randomBytes(500, 10)
	newData := randomBytes(500, 11)
	sig, err := BuildSignature(old, bs)
	if err != nil {
		t.Fatalf("BuildSignature: %v", err)
	}
	p1, _, err := GenerateDelta(sig, newData)
	if err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	p2, _, err := GenerateDelta(sig, newData)
	if err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	if !bytes.Equal(p1, p2) {
		t.Fatalf("same input produced different patches: %s != %s", shortHash(p1), shortHash(p2))
	}
	t.Logf("input: old=%dB(sha=%s) new=%dB(sha=%s); two runs both produced "+
		"patch=%dB(sha=%s): deterministic", len(old), shortHash(old),
		len(newData), shortHash(newData), len(p1), shortHash(p1))
}

func TestInvalidBlockSize(t *testing.T) {
	for _, bs := range []int{0, -1, -100} {
		if _, err := BuildSignature([]byte("abc"), bs); !errors.Is(err, ErrInvalidBlockSize) {
			t.Fatalf("BuildSignature(bs=%d): got %v, want ErrInvalidBlockSize", bs, err)
		}
		sig := &Signature{BlockSize: bs}
		if _, _, err := GenerateDelta(sig, []byte("abc")); !errors.Is(err, ErrInvalidBlockSize) {
			t.Fatalf("GenerateDelta(bs=%d): got %v, want ErrInvalidBlockSize", bs, err)
		}
		t.Logf("input bs=%d -> rejected with ErrInvalidBlockSize", bs)
	}
}
