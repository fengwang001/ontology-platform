package patch_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
	"ontology/verify"
)

func makePatch(t *testing.T, src, target []byte, bs int) []byte {
	t.Helper()
	sg, err := sig.Generate(target, bs)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := diff.Diff(src, sg)
	if err != nil {
		t.Fatal(err)
	}
	return p.Encode()
}

func classify(err error) string {
	switch {
	case errors.Is(err, verify.ErrHeaderTruncated):
		return "header"
	case errors.Is(err, verify.ErrOpTruncated):
		return "op"
	case errors.Is(err, verify.ErrDataTruncated):
		return "data"
	case errors.Is(err, verify.ErrCRCMismatch):
		return "crc"
	}
	return "other"
}

func TestTruncationEveryByte(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	const bs = 64
	target := make([]byte, 10*bs)
	rng.Read(target)
	src := append([]byte(nil), target...)
	rng.Read(src[5*bs : 6*bs]) // 产生 复用+数据+复用 的混合指令
	raw := makePatch(t, src, target, bs)
	before := append([]byte(nil), target...)
	seen := map[string]int{}
	for cut := 1; cut < len(raw); cut++ {
		truncated := raw[:cut]
		out, err := patch.Apply(target, truncated)
		if err == nil {
			t.Fatalf("cut=%d: truncated patch applied without error", cut)
		}
		if out != nil {
			t.Fatalf("cut=%d: failed apply must not return data", cut)
		}
		cls := classify(err)
		if cls == "other" {
			t.Fatalf("cut=%d: unclassified error %v", cut, err)
		}
		seen[cls]++
		if !bytes.Equal(target, before) {
			t.Fatalf("cut=%d: target mutated after failed apply", cut)
		}
	}
	for _, cls := range []string{"header", "op", "data", "crc"} {
		if seen[cls] == 0 {
			t.Fatalf("truncation class %q never observed in %d cuts", cls, len(raw)-1)
		}
	}
	t.Logf("patch len=%d cuts by class: %v", len(raw), seen)
}

func TestReuseIndexOutOfRange(t *testing.T) {
	target := []byte("0123456789abcdef")
	p := diff.Patch{BlockSize: 4, SrcLen: 4, Ops: []diff.Op{{Reuse: true, Index: 99}}}
	out, err := patch.Apply(target, p.Encode())
	if !errors.Is(err, verify.ErrBlockRange) {
		t.Fatalf("err=%v want ErrBlockRange", err)
	}
	if out != nil {
		t.Fatal("must not return data on range error")
	}
}

func TestSignatureDataMismatch(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	const bs = 64
	target := make([]byte, 20*bs)
	rng.Read(target)
	sg, err := sig.Generate(target, bs) // 先生成签名
	if err != nil {
		t.Fatal(err)
	}
	src := append([]byte(nil), target...)
	rng.Read(src[7*bs : 8*bs])
	p, _, err := diff.Diff(src, sg)
	if err != nil {
		t.Fatal(err)
	}
	raw := p.Encode()
	target[0] ^= 0xff // 签名生成后、应用前目标端数据被改动
	out, err := patch.Apply(target, raw)
	if !errors.Is(err, verify.ErrStateMismatch) {
		t.Fatalf("err=%v want ErrStateMismatch", err)
	}
	if out != nil {
		t.Fatal("must not return wrong data on state mismatch")
	}
}

func TestApplyRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	const bs = 64
	target := make([]byte, 50*bs)
	rng.Read(target)
	src := append([]byte(nil), target...)
	rng.Read(src[10*bs : 12*bs])
	out, err := patch.Apply(target, makePatch(t, src, target, bs))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, src) {
		t.Fatal("applied result != src")
	}
}
