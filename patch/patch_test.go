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

// fixture 构造「中间差一块」场景：目标 40 字节、补丁 89 字节
// （头 52 + 指令 5×5 + 数据段 8 + CRC 4）。
func fixture(t *testing.T) (tgt, raw []byte, p diff.Patch) {
	t.Helper()
	const bs = 8
	tgt = make([]byte, 40)
	rand.New(rand.NewSource(99)).Read(tgt)
	src := append([]byte(nil), tgt...)
	for k := 2 * bs; k < 3*bs; k++ {
		src[k] ^= 0xA5
	}
	sg, err := sig.Generate(tgt, bs)
	if err != nil {
		t.Fatal(err)
	}
	p = diff.Diff(src, sg)
	raw = patch.Encode(p)
	if len(raw) != 89 {
		t.Fatalf("夹具补丁长度 %d，期望 89", len(raw))
	}
	if _, err := patch.Decode(raw); err != nil {
		t.Fatalf("完整补丁应可解码: %v", err)
	}
	return tgt, raw, p
}

// 故障注入：从 1 截断到文件长度减一，每个截断点都要被分类为四类之一，
// 且截断的补丁绝不允许被部分应用（目标数据逐字节不变）。
func TestTruncation(t *testing.T) {
	tgt, raw, _ := fixture(t)
	before := append([]byte(nil), tgt...)
	sentinels := []error{
		patch.ErrTruncatedHeader, patch.ErrTruncatedInstr,
		patch.ErrTruncatedData, patch.ErrCRCMismatch,
	}
	// 边界抽样：区间端点必须落在预期分类（区间实测见 FINDINGS.md）。
	bounds := []struct {
		n    int
		want error
	}{
		{1, patch.ErrTruncatedHeader}, {51, patch.ErrTruncatedHeader},
		{52, patch.ErrTruncatedInstr}, {76, patch.ErrTruncatedInstr},
		{77, patch.ErrTruncatedData}, {84, patch.ErrTruncatedData},
		{85, patch.ErrCRCMismatch}, {88, patch.ErrCRCMismatch},
	}
	for _, b := range bounds {
		if _, err := patch.Decode(raw[:b.n]); !errors.Is(err, b.want) {
			t.Fatalf("截断到 %d：得到 %v，期望 %v", b.n, err, b.want)
		}
	}
	seen := map[error]int{}
	for n := 1; n < len(raw); n++ {
		dec, err := patch.Decode(raw[:n])
		if err == nil {
			t.Fatalf("截断到 %d 竟然解码成功", n)
		}
		matched := false
		for _, s := range sentinels {
			if errors.Is(err, s) {
				seen[s]++
				matched = true
			}
		}
		if !matched {
			t.Fatalf("截断到 %d 的错误不可判定: %v", n, err)
		}
		if _, err := patch.Apply(tgt, dec); err == nil {
			t.Fatalf("截断到 %d 的补丁被应用", n)
		}
		if !bytes.Equal(tgt, before) {
			t.Fatalf("截断到 %d 后目标数据被改动", n)
		}
	}
	if len(seen) != len(sentinels) {
		t.Fatalf("四类截断未全部出现: %v", seen)
	}
}

// 故障注入：复用块指令的块号越界，必须检出且不得 panic。
func TestBlockOutOfRange(t *testing.T) {
	tgt, _, p := fixture(t)
	before := append([]byte(nil), tgt...)
	cases := []struct {
		name  string
		index int
	}{
		{"刚好越界", 5},
		{"远超范围", 9999},
		{"负值", -1},
	}
	for _, c := range cases {
		bad := diff.Patch{BlockSize: p.BlockSize, SrcLen: p.SrcLen, SrcHash: p.SrcHash}
		bad.Instrs = append(append([]diff.Instr{}, p.Instrs...), diff.Instr{Reuse: true, Index: c.index})
		dec, err := patch.Decode(patch.Encode(bad)) // 编码合法、CRC 正确
		if err != nil {
			t.Fatalf("%s：解码不应失败: %v", c.name, err)
		}
		if _, err := patch.Apply(tgt, dec); !errors.Is(err, patch.ErrBlockOutOfRange) {
			t.Fatalf("%s：得到 %v，期望 ErrBlockOutOfRange", c.name, err)
		}
		if !bytes.Equal(tgt, before) {
			t.Fatalf("%s：目标数据被改动", c.name)
		}
	}
}

// 故障注入：签名生成后目标端改动了数据，最终校验必须检出而不是产出错误数据。
func TestSignatureDataMismatch(t *testing.T) {
	tgt, _, p := fixture(t)
	tampered := append([]byte(nil), tgt...)
	tampered[0] ^= 0xFF // 签名生成后目标端自行改动
	out, err := patch.Apply(tampered, p)
	if err != nil {
		t.Fatalf("应用本身应成功: %v", err)
	}
	if err := verify.Final(out, p); !errors.Is(err, verify.ErrChecksumMismatch) {
		t.Fatalf("得到 %v，期望 ErrChecksumMismatch", err)
	}
}
