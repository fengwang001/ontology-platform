package verify_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/chunk"
	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
	"ontology/verify"
)

func buildPatch(t *testing.T, src, tgt []byte, bs int) ([]byte, *diff.Patch) {
	t.Helper()
	s, err := sig.Build(tgt, bs)
	if err != nil {
		t.Fatal(err)
	}
	p := diff.Diff(src, s)
	return p.Encode(), p
}

// 对补丁逐字节从 1 截断到 len-1：每个截断点都必须被分类为头部 / 指令 /
// 数据段不完整或 CRC 不匹配，且目标端数据逐字节不变（不得部分应用）。
func TestTruncation(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	const bs = 16
	tgt := randBytes(rng, 8*bs)
	src := bytes.Clone(tgt)
	rng.Read(src[3*bs : 4*bs]) // 中间差一块：补丁含复用与新数据两类指令
	patchBytes, p := buildPatch(t, src, tgt, bs)
	if p.NewDataBytes() == 0 || p.ReusedBlocks() == 0 {
		t.Fatal("test patch must contain both copy and literal instructions")
	}
	// 计算每条指令的字节区间，定位新数据段。
	type span struct{ lo, hi int }
	var dataSpans []span
	pos := diff.HeaderLen
	for _, in := range p.Instrs {
		pos += 5 // op + 定长参数
		if in.Kind == diff.Literal {
			dataSpans = append(dataSpans, span{pos, pos + len(in.Data)})
			pos += len(in.Data)
		}
	}
	wantErr := func(cut int) error {
		switch {
		case cut < diff.HeaderLen:
			return verify.ErrTruncatedHeader
		case cut >= len(patchBytes)-4:
			return verify.ErrCRCMismatch
		}
		for _, sp := range dataSpans {
			if cut >= sp.lo && cut < sp.hi {
				return verify.ErrTruncatedData
			}
		}
		return verify.ErrTruncatedInstruction
	}
	before := bytes.Clone(tgt)
	seen := map[error]int{}
	for cut := 1; cut < len(patchBytes); cut++ {
		out, err := patch.Apply(tgt, patchBytes[:cut])
		want := wantErr(cut)
		if !errors.Is(err, want) {
			t.Fatalf("cut=%d: err=%v want %v", cut, err, want)
		}
		if out != nil {
			t.Fatalf("cut=%d: truncated patch must not produce output", cut)
		}
		if !bytes.Equal(tgt, before) {
			t.Fatalf("cut=%d: target modified by failed apply", cut)
		}
		seen[want]++
	}
	for _, e := range []error{verify.ErrTruncatedHeader, verify.ErrTruncatedInstruction,
		verify.ErrTruncatedData, verify.ErrCRCMismatch} {
		if seen[e] == 0 {
			t.Fatalf("class %v never observed", e)
		}
	}
	t.Logf("classes: header=%d instr=%d data=%d crc=%d",
		seen[verify.ErrTruncatedHeader], seen[verify.ErrTruncatedInstruction],
		seen[verify.ErrTruncatedData], seen[verify.ErrCRCMismatch])
}

// 复用块指令的块号越界必须检出，不得 panic 或读到别的数据。
func TestCopyOutOfRange(t *testing.T) {
	tgt := []byte("0123456789abcdef")
	cases := []uint32{2, 100, 1<<31 - 1}
	for _, idx := range cases {
		p := &diff.Patch{
			BlockSize: 16,
			SrcLen:    16,
			SrcStrong: chunk.SumStrong(tgt),
			Instrs:    []diff.Instr{{Kind: diff.Copy, Index: idx}},
		}
		out, err := patch.Apply(tgt, p.Encode())
		if !errors.Is(err, verify.ErrBlockOutOfRange) {
			t.Fatalf("idx=%d: err=%v want ErrBlockOutOfRange", idx, err)
		}
		if out != nil {
			t.Fatalf("idx=%d: must not produce output", idx)
		}
	}
}

// 目标端生成签名后、应用补丁前改动数据：应用必须检出结果与源端不一致。
func TestTargetChangedAfterSign(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	tgt := randBytes(rng, 128)
	src := bytes.Clone(tgt)
	rng.Read(src[32:48])
	patchBytes, _ := buildPatch(t, src, tgt, 16)
	tgt[80] ^= 0xFF // 签名生成后目标端数据被改动
	out, err := patch.Apply(tgt, patchBytes)
	if !errors.Is(err, verify.ErrChecksumMismatch) {
		t.Fatalf("err=%v want ErrChecksumMismatch", err)
	}
	if out != nil {
		t.Fatal("must not return wrong data")
	}
}

// 各类错误可用 errors.Is 区分；块大小为 0 是可判定错误。
func TestErrorKinds(t *testing.T) {
	all := []error{
		verify.ErrTruncatedHeader, verify.ErrTruncatedInstruction,
		verify.ErrTruncatedData, verify.ErrCRCMismatch,
		verify.ErrBlockOutOfRange, verify.ErrChecksumMismatch,
		chunk.ErrInvalidBlockSize,
	}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("errors %v and %v must be distinguishable", a, b)
			}
		}
	}
	if _, err := sig.Build([]byte("x"), 0); !errors.Is(err, chunk.ErrInvalidBlockSize) {
		t.Fatalf("block size 0: err=%v", err)
	}
	if _, err := chunk.NumBlocks(10, 0); !errors.Is(err, chunk.ErrInvalidBlockSize) {
		t.Fatalf("NumBlocks size 0: err=%v", err)
	}
}

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}
