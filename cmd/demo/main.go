// Command demo 演示差分数据同步的校验与增量传输。
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"

	"ontology/chunk"
	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
	"ontology/verify"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK   " + name)
		return
	}
	failed++
	fmt.Println("FAIL " + name)
}

// syncOnce 跑一遍 签名→diff→编码→应用，返回结果与统计。
func syncOnce(src, tgt []byte, bs int) ([]byte, diff.Stats, error) {
	sg, err := sig.Build(tgt, bs)
	if err != nil {
		return nil, diff.Stats{}, err
	}
	p, st, err := diff.Compute(src, sg)
	if err != nil {
		return nil, st, err
	}
	out, err := patch.Apply(tgt, patch.Encode(p))
	return out, st, err
}

func main() {
	rng := rand.New(rand.NewSource(7))
	data := make([]byte, 300)
	rng.Read(data)
	const bs = 16
	dir, err := os.MkdirTemp("", "ontsync")
	if err != nil {
		fmt.Println("FAIL 无法创建临时目录")
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	// chunk：滚动校验与逐偏移重算完全一致。
	r := chunk.NewRoller(data[:bs])
	eq := true
	for i := 0; i+bs <= len(data); i++ {
		if r.Sum() != chunk.WeakSum(data[i:i+bs]) {
			eq = false
			break
		}
		if i+bs < len(data) {
			r.Roll(data[i], data[i+bs])
		}
	}
	check("滚动校验与逐偏移重算完全一致", eq)

	// chunk：弱校验和基本运算次数不超过 4 倍数据长度。
	chunk.ResetWeakOps()
	r2 := chunk.NewRoller(data[:bs])
	for i := 0; i+bs < len(data); i++ {
		r2.Roll(data[i], data[i+bs])
	}
	check("弱校验基本运算次数不超过 4 倍数据长度", chunk.WeakOps() <= 4*uint64(len(data)))

	// sig：签名编码后解码往返一致。
	sg, sgErr := sig.Build(data, bs)
	back, decErr := sig.Decode(sg.Encode())
	check("签名编解码往返一致", sgErr == nil && decErr == nil && reflect.DeepEqual(sg, back))

	// verify：整体强校验和能检出结果被篡改。
	tampered := append([]byte(nil), data...)
	tampered[0] ^= 0xFF
	err = verify.CheckResult(chunk.Strong(data), tampered)
	check("结果整体强校验可检出篡改", errors.Is(err, verify.ErrMismatch))

	// diff：弱校验和碰撞被强校验挡住，结果仍与源端一致。
	outC, stC, err := syncOnce([]byte{4, 3, 2, 1}, []byte{1, 2, 3, 4}, 4)
	check("弱校验和碰撞被强校验挡住且结果正确",
		err == nil && stC.ReusedBlocks == 0 && bytes.Equal(outC, []byte{4, 3, 2, 1}))

	// diff：源与目标只差中间一个块，新数据不超过 2 倍块大小。
	tgtM := make([]byte, 64)
	for i := range tgtM {
		tgtM[i] = byte(i/8 + 1)
	}
	srcM := append([]byte(nil), tgtM...)
	for i := 32; i < 40; i++ {
		srcM[i] = 0x55
	}
	outM, stM, err := syncOnce(srcM, tgtM, 8)
	check("中间差一块时新数据不超 2 倍块大小",
		err == nil && bytes.Equal(outM, srcM) && stM.NewBytes <= 2*8)

	// diff：完全相同时新数据为 0。
	outI, stI, err := syncOnce(tgtM, tgtM, 8)
	check("完全相同时新数据为 0", err == nil && bytes.Equal(outI, tgtM) && stI.NewBytes == 0)

	// diff：强校验只在弱命中时计算。
	check("强校验次数不超弱匹配次数", stM.StrongChecks <= stM.WeakMatches)

	// 双方数据与补丁写入本地临时目录。
	sgM, _ := sig.Build(tgtM, 8)
	pM, _, _ := diff.Compute(srcM, sgM)
	os.WriteFile(filepath.Join(dir, "source.bin"), srcM, 0o644)
	os.WriteFile(filepath.Join(dir, "target.bin"), tgtM, 0o644)
	os.WriteFile(filepath.Join(dir, "sample.patch"), patch.Encode(pM), 0o644)

	// patch：构造一份含复用与新数据指令的补丁（目标 3 块，源 11 字节）。
	tgtB := []byte("AAAABBBBCCCC")
	srcB := []byte("AAAAXYBBBBZ")
	wire := patch.Encode(patch.Patch{
		BlockSize: 4, SrcLen: len(srcB), SrcHash: chunk.Strong(srcB),
		Instrs: []patch.Instr{
			{Op: patch.OpReuse, Block: 0},
			{Op: patch.OpData, Data: []byte("XY")},
			{Op: patch.OpReuse, Block: 1},
			{Op: patch.OpData, Data: []byte("Z")},
		},
	})

	// patch：逐截断点遍历，四类截断错误各至少出现一例。
	classes := 0
	allErr := true
	for cut := 1; cut < len(wire); cut++ {
		_, err := patch.Apply(tgtB, wire[:cut])
		if err == nil {
			allErr = false
		}
		for i, sentinel := range []error{verify.ErrHeaderIncomplete, verify.ErrInstrIncomplete,
			verify.ErrDataIncomplete, verify.ErrCRCMismatch} {
			if errors.Is(err, sentinel) {
				classes |= 1 << i
			}
		}
	}
	check("四类截断分类各至少出现一例", classes == 0b1111 && allErr)

	// patch：截断的补丁绝不被部分应用，目标文件逐字节不变。
	tp := filepath.Join(dir, "target.bin")
	pp := filepath.Join(dir, "patch.bin")
	os.WriteFile(tp, tgtB, 0o644)
	os.WriteFile(pp, wire[:len(wire)/2], 0o644)
	applyErr := patch.ApplyFile(tp, pp)
	after, _ := os.ReadFile(tp)
	check("截断补丁未被部分应用", applyErr != nil && bytes.Equal(after, tgtB))

	// patch：复用块号越界被检出（修好 CRC 以排除截断干扰）。
	bad := append([]byte(nil), wire...)
	binary.BigEndian.PutUint32(bad[37:41], 999)
	binary.BigEndian.PutUint32(bad[len(bad)-4:], crc32.ChecksumIEEE(bad[:len(bad)-4]))
	_, err = patch.Apply(tgtB, bad)
	check("复用块越界被检出", errors.Is(err, verify.ErrOutOfRange))

	// patch：签名生成后目标数据被改动，整体强校验检出。
	reuseAll := patch.Encode(patch.Patch{
		BlockSize: 4, SrcLen: 4, SrcHash: chunk.Strong([]byte("AAAA")),
		Instrs: []patch.Instr{{Op: patch.OpReuse, Block: 0}},
	})
	_, err = patch.Apply([]byte("XAAABBBBCCCC"), reuseAll)
	check("签名与目标数据不符被检出", errors.Is(err, verify.ErrMismatch))

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		panic("checks failed")
	}
}
