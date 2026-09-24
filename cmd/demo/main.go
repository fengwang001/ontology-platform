// Command demo 演示差分同步：弱/强校验分工、滚动正确性、补丁最小化、
// 故障注入检出。不读参数不联网，全部判定通过时退出码为 0。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"ontology/chunk"
	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
	"ontology/verify"
)

var failures int
var tmpDir string

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	var err error
	tmpDir, err = os.MkdirTemp("", "ontology-demo")
	if err != nil {
		fmt.Println("FAIL 创建临时目录:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)
	checkRoll()
	checkCollision()
	checkMinimal()
	checkIdentical()
	checkStrongCount()
	checkTruncation()
	checkOutOfRange()
	checkTargetChanged()
	fmt.Printf("总计 %d 项失败\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}

// roundTrip 把源、目标与补丁写入临时目录并回放，返回补丁与应用结果。
func roundTrip(src, tgt []byte, bs int) (*diff.Patch, []byte, error) {
	s, err := sig.Build(tgt, bs)
	if err != nil {
		return nil, nil, err
	}
	p := diff.Diff(src, s)
	patchBytes := p.Encode()
	files := map[string][]byte{"src.bin": src, "tgt.bin": tgt, "patch.bin": patchBytes}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), data, 0o600); err != nil {
			return nil, nil, err
		}
	}
	out, err := patch.Apply(tgt, patchBytes)
	return p, out, err
}

// 滚动校验和从偏移 i 推进到 i+1 必须与在 i+1 处从头重算相等。
func checkRoll() {
	rng := rand.New(rand.NewSource(42))
	data := make([]byte, 512)
	rng.Read(data)
	const bs = 16
	ok := true
	w := chunk.SumWeak(data[:bs])
	for i := 0; i+bs < len(data); i++ {
		w = w.Roll(data[i], data[i+bs], bs)
		ok = ok && w == chunk.SumWeak(data[i+1:i+1+bs])
	}
	check("滚动校验和与逐偏移重算完全相等", ok)
}

// 弱校验和碰撞（[1,0,0,1] vs [0,1,1,0]）必须被强校验和挡住。
func checkCollision() {
	tgt := []byte{1, 0, 0, 1}
	src := []byte{0, 1, 1, 0}
	p, out, err := roundTrip(src, tgt, 4)
	ok := err == nil && bytes.Equal(out, src) && p.ReusedBlocks() == 0 &&
		p.Stats.WeakMatches > 0
	check("弱校验和碰撞被强校验和挡住且结果正确", ok)
}

// 源与目标只差中间一个块时，新数据字节数不超过 2 倍块大小。
func checkMinimal() {
	rng := rand.New(rand.NewSource(5))
	const bs = 32
	tgt := make([]byte, 8*bs)
	rng.Read(tgt)
	src := bytes.Clone(tgt)
	rng.Read(src[3*bs : 4*bs])
	p, out, err := roundTrip(src, tgt, bs)
	ok := err == nil && bytes.Equal(out, src) && p.NewDataBytes() <= 2*bs
	check("中间差一块时新数据不超过 2 倍块大小", ok)
}

// 完全相同时新数据字节数为 0。
func checkIdentical() {
	rng := rand.New(rand.NewSource(6))
	data := make([]byte, 200)
	rng.Read(data)
	p, out, err := roundTrip(data, data, 16)
	ok := err == nil && bytes.Equal(out, data) && p.NewDataBytes() == 0
	check("完全相同时新数据为 0", ok)
}

// 强校验和只在弱命中时计算：StrongComputed 不超过 WeakMatches。
func checkStrongCount() {
	rng := rand.New(rand.NewSource(8))
	tgt := make([]byte, 300)
	rng.Read(tgt)
	src := bytes.Clone(tgt)
	rng.Read(src[100:150])
	p, _, err := roundTrip(src, tgt, 16)
	ok := err == nil && p.Stats.StrongComputed <= p.Stats.WeakMatches &&
		p.Stats.WeakMatches > 0
	check("强校验次数不超过弱匹配次数", ok)
}

// 四类截断各取一例均被正确分类，且截断补丁未被部分应用。
func checkTruncation() {
	rng := rand.New(rand.NewSource(21))
	const bs = 16
	tgt := make([]byte, 8*bs)
	rng.Read(tgt)
	src := bytes.Clone(tgt)
	rng.Read(src[3*bs : 4*bs])
	s, _ := sig.Build(tgt, bs)
	p := diff.Diff(src, s)
	patchBytes := p.Encode()
	// 按指令布局定位首个新数据段的中点。
	dataCut := -1
	pos := diff.HeaderLen
	for _, in := range p.Instrs {
		pos += 5
		if in.Kind == diff.Literal {
			dataCut = pos + len(in.Data)/2
			break
		}
	}
	before := bytes.Clone(tgt)
	cuts := []struct {
		at   int
		want error
	}{
		{10, verify.ErrTruncatedHeader},
		{diff.HeaderLen + 2, verify.ErrTruncatedInstruction},
		{dataCut, verify.ErrTruncatedData},
		{len(patchBytes) - 2, verify.ErrCRCMismatch},
	}
	ok := true
	for _, c := range cuts {
		out, err := patch.Apply(tgt, patchBytes[:c.at])
		if !errors.Is(err, c.want) || out != nil || !bytes.Equal(tgt, before) {
			ok = false
		}
	}
	check("四类截断分类各一例且未被部分应用", ok)
}

// 复用块指令块号越界必须被检出。
func checkOutOfRange() {
	tgt := []byte("0123456789abcdef")
	p := &diff.Patch{
		BlockSize: 16,
		SrcLen:    16,
		SrcStrong: chunk.SumStrong(tgt),
		Instrs:    []diff.Instr{{Kind: diff.Copy, Index: 99}},
	}
	out, err := patch.Apply(tgt, p.Encode())
	check("指令越界被检出", errors.Is(err, verify.ErrBlockOutOfRange) && out == nil)
}

// 签名生成后目标端改动数据，应用必须检出结果不一致。
func checkTargetChanged() {
	rng := rand.New(rand.NewSource(23))
	tgt := make([]byte, 128)
	rng.Read(tgt)
	src := bytes.Clone(tgt)
	rng.Read(src[32:48])
	s, _ := sig.Build(tgt, 16)
	patchBytes := diff.Diff(src, s).Encode()
	tgt[80] ^= 0xFF
	out, err := patch.Apply(tgt, patchBytes)
	check("签名与目标数据不符被检出", errors.Is(err, verify.ErrChecksumMismatch) && out == nil)
}
