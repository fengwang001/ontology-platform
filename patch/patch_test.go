package patch

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ontology/diff"
	"ontology/sig"
	"ontology/verify"
)

func seq(n int, start byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

// TestRelations 六种源/目标关系：断言新数据量、复用块数及结果与源一致。
func TestRelations(t *testing.T) {
	base := seq(16, 1)
	long := seq(20, 1)
	mid := append([]byte{}, base...)
	for i := 4; i < 8; i++ {
		mid[i] = 99 - byte(i)
	}
	cases := []struct {
		name     string
		src      []byte
		target   []byte
		wantLit  int
		wantCopy int
		maxLit   int
	}{
		{"相同", base, append([]byte{}, base...), 0, 4, 0},
		{"中间差一块", mid, append([]byte{}, base...), 4, 3, 8},
		{"目标为源前缀", long, append([]byte{}, long[:12]...), 8, 3, 0},
		{"源为目标前缀", append([]byte{}, long[:12]...), long, 0, 3, 0},
		{"完全不同", bytes.Repeat([]byte{0x20}, 16), bytes.Repeat([]byte{0x10}, 16), 16, 0, 0},
		{"空", nil, nil, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sg, _ := sig.Generate(tc.target, 4)
			res, err := diff.Build(tc.src, sg)
			if err != nil {
				t.Fatal(err)
			}
			if res.LiteralBytes != tc.wantLit || res.CopyCount != tc.wantCopy {
				t.Fatalf("lit=%d want %d copy=%d want %d", res.LiteralBytes, tc.wantLit, res.CopyCount, tc.wantCopy)
			}
			if tc.maxLit > 0 && res.LiteralBytes > tc.maxLit {
				t.Fatalf("lit=%d > 2n=%d", res.LiteralBytes, tc.maxLit)
			}
			out, err := ApplyBytes(tc.target, res.Patch)
			if err != nil || !bytes.Equal(out, tc.src) {
				t.Fatalf("apply err=%v match=%v", err, bytes.Equal(out, tc.src))
			}
		})
	}
}

// TestStrongLeqWeak 强和次数不超过弱命中次数。
func TestStrongLeqWeak(t *testing.T) {
	src := seq(40, 1)
	target := append([]byte{}, src...)
	target[10] ^= 0xFF
	sg, _ := sig.Generate(target, 4)
	res, err := diff.Build(src, sg)
	if err != nil {
		t.Fatal(err)
	}
	if res.StrongCalls > res.WeakHits {
		t.Fatalf("strong=%d weakHits=%d", res.StrongCalls, res.WeakHits)
	}
}

// TestTruncationEveryByte 逐字节截断：四类可判定错误，且目标文件永不被部分改动。
func TestTruncationEveryByte(t *testing.T) {
	dir := t.TempDir()
	target := seq(16, 1)
	src := append([]byte{}, target...)
	src[5] = 77
	sg, _ := sig.Generate(target, 4)
	res, _ := diff.Build(src, sg)
	full := res.Patch.Encode()
	tp := filepath.Join(dir, "t.bin")
	pp := filepath.Join(dir, "p.bin")
	seen := map[error]bool{}
	for cut := 1; cut <= len(full); cut++ {
		var raw []byte
		if cut < len(full) {
			raw = full[:cut]
		} else {
			raw = append([]byte{}, full...)
			raw[len(raw)-1] ^= 0xFF // 长度完整但 CRC 损坏
		}
		if err := os.WriteFile(tp, target, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pp, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		err := ApplyFile(tp, pp)
		if err == nil {
			t.Fatalf("cut %d: expected error", cut)
		}
		got, err2 := os.ReadFile(tp)
		if err2 != nil || !bytes.Equal(got, target) {
			t.Fatalf("cut %d: target partially modified", cut)
		}
		switch {
		case errors.Is(err, verify.ErrHeaderTruncated), errors.Is(err, verify.ErrInstrTruncated),
			errors.Is(err, verify.ErrDataTruncated), errors.Is(err, verify.ErrCRC):
			seen[err] = true
		default:
			t.Fatalf("cut %d: unclassified error %v", cut, err)
		}
	}
	for _, e := range []error{verify.ErrHeaderTruncated, verify.ErrInstrTruncated, verify.ErrDataTruncated, verify.ErrCRC} {
		if !seen[e] {
			t.Fatalf("category not observed: %v", e)
		}
	}
}

// TestBlockIndexOutOfRange 复用块号越界必须检出，不 panic、不读越界数据。
func TestBlockIndexOutOfRange(t *testing.T) {
	sg, _ := sig.Generate(seq(16, 1), 4)
	res, _ := diff.Build(seq(16, 1), sg)
	for _, idx := range []uint32{4, 1_000_000} {
		p := res.Patch
		p.Instrs = []verify.Instr{{Op: verify.OpCopy, Idx: idx}}
		if _, err := ApplyBytes(seq(16, 1), p); !errors.Is(err, verify.ErrBlockIndex) {
			t.Fatalf("idx=%d err=%v want ErrBlockIndex", idx, err)
		}
	}
}

// TestSignatureDataMismatch 生成签名后改动目标数据，必须检出结果与源不一致。
func TestSignatureDataMismatch(t *testing.T) {
	src := seq(16, 1)
	original := seq(16, 1)
	sg, _ := sig.Generate(original, 4)
	res, _ := diff.Build(src, sg)
	mutated := append([]byte{}, original...)
	mutated[0] ^= 0xFF
	if _, err := ApplyBytes(mutated, res.Patch); !errors.Is(err, verify.ErrResultMismatch) {
		t.Fatalf("flip err=%v want ErrResultMismatch", err)
	}
}

// TestDeterministic 同一输入跑 20 次，补丁逐字节相同。
func TestDeterministic(t *testing.T) {
	src := seq(31, 3)
	target := seq(31, 1)
	target[7] = 50
	sg, _ := sig.Generate(target, 4)
	first, _ := diff.Build(src, sg)
	want := first.Patch.Encode()
	for i := 0; i < 20; i++ {
		r, _ := diff.Build(src, sg)
		if !bytes.Equal(r.Patch.Encode(), want) {
			t.Fatalf("run %d produced different patch", i)
		}
	}
}

// TestConcurrentDiff 多协程对不同数据对并发 diff，互不干扰。
func TestConcurrentDiff(t *testing.T) {
	const workers = 32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			src := seq(23, byte(w))
			target := seq(23, byte(w+1))
			sg, _ := sig.Generate(target, 4)
			res, err := diff.Build(src, sg)
			if err != nil {
				t.Errorf("worker %d: %v", w, err)
				return
			}
			out, err := ApplyBytes(target, res.Patch)
			if err != nil || !bytes.Equal(out, src) {
				t.Errorf("worker %d apply fail err=%v", w, err)
			}
		}(w)
	}
	wg.Wait()
}

// TestEdgeApply 空源/目标、块大于数据、长度恰为整数倍等边界。
func TestEdgeApply(t *testing.T) {
	cases := []struct {
		name   string
		src    []byte
		target []byte
		n      uint32
	}{
		{"empty-src", nil, seq(4, 1), 4},
		{"empty-target", seq(4, 1), nil, 4},
		{"block-bigger", []byte{1, 2}, []byte{3, 4}, 8},
		{"exact-multiple", seq(8, 1), seq(8, 2), 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sg, _ := sig.Generate(tc.target, tc.n)
			res, err := diff.Build(tc.src, sg)
			if err != nil {
				t.Fatal(err)
			}
			out, err := ApplyBytes(tc.target, res.Patch)
			if err != nil || !bytes.Equal(out, tc.src) {
				t.Fatalf("err=%v equal=%v", err, bytes.Equal(out, tc.src))
			}
		})
	}
}

// TestZeroBlockSize 块大小 0 在生成签名阶段即可判定。
func TestZeroBlockSize(t *testing.T) {
	if _, err := sig.Generate([]byte{1}, 0); !errors.Is(err, verify.ErrBadBlockSize) {
		t.Fatalf("sig err=%v", err)
	}
}
