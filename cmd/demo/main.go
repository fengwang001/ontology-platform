// Command demo 演示差分同步的校验与增量传输：逐条打印判定，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"

	"ontology/chunk"
	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
	"ontology/verify"
)

var failures int

func check(ok bool, name string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	checks := 0
	// chunk：滚动校验和与从头重算在每个偏移相等，且代价 O(n)。
	{
		data := make([]byte, 4096)
		rand.New(rand.NewSource(7)).Read(data)
		const bs = 32
		chunk.ResetCounters()
		w := chunk.WeakSum(data[:bs])
		rolled := []uint32{w}
		for i := 0; i+bs < len(data); i++ {
			w = chunk.Roll(w, data[i], data[i+bs])
			rolled = append(rolled, w)
		}
		ops := chunk.WeakOps()
		ok := true
		for i := 0; i+bs <= len(data); i++ {
			if rolled[i] != chunk.WeakSum(data[i:i+bs]) {
				ok = false
			}
		}
		ok = ok && ops <= int64(4*len(data))
		check(ok, "滚动校验和逐偏移等于重算且代价<=4n")
		checks++
	}
	// sig：签名编解码往返一致；块大小为 0 是可判定错误。
	{
		data := make([]byte, 100)
		rand.New(rand.NewSource(11)).Read(data)
		sg, err := sig.Generate(data, 16)
		ok := err == nil && len(sg.Blocks) == 7 && sg.BlockLen(6) == 4
		rt, err2 := sig.Decode(sig.Encode(sg))
		ok = ok && err2 == nil && rt.BlockSize == sg.BlockSize && rt.TotalLen == sg.TotalLen &&
			len(rt.Blocks) == len(sg.Blocks) && rt.Blocks[3] == sg.Blocks[3]
		_, err3 := sig.Generate(data, 0)
		ok = ok && errors.Is(err3, sig.ErrZeroBlockSize)
		check(ok, "签名编解码往返一致且块大小为0可判定")
		checks++
	}
	// diff：中间差一块时新数据 ≤ 2 倍块大小；完全相同时为 0；强校验次数 ≤ 弱匹配次数。
	{
		const bs = 16
		tgt := make([]byte, 5*bs)
		rand.New(rand.NewSource(23)).Read(tgt)
		src := make([]byte, len(tgt))
		copy(src, tgt)
		for k := 2 * bs; k < 3*bs; k++ { // 改掉中间一整块
			src[k] ^= 0x5A
		}
		sg, _ := sig.Generate(tgt, bs)
		chunk.ResetCounters()
		p := diff.Diff(src, sg)
		check(p.NewBytes() <= 2*bs, "中间差一块时新数据不超过2倍块大小")
		checks++
		check(chunk.StrongOps() <= int64(p.WeakHits), "强校验次数不超过弱匹配次数")
		checks++
		chunk.ResetCounters()
		pSame := diff.Diff(tgt, sg)
		check(pSame.NewBytes() == 0 && pSame.ReuseCount() == 5, "完全相同时新数据为0")
		checks++
	}
	// patch：四类截断各一例均可判定；截断补丁绝不部分应用；指令越界被检出。
	{
		const bs = 16
		tgt := make([]byte, 4*bs)
		rand.New(rand.NewSource(31)).Read(tgt)
		src := make([]byte, 4*bs)
		rand.New(rand.NewSource(32)).Read(src)
		sg, _ := sig.Generate(tgt, bs)
		raw := patch.Encode(diff.Diff(src, sg))
		before := append([]byte(nil), tgt...)
		classOf := func(n int) error {
			_, err := patch.Decode(raw[:n])
			return err
		}
		check(errors.Is(classOf(10), patch.ErrTruncatedHeader), "截断分类:头部不完整")
		checks++
		check(errors.Is(classOf(53), patch.ErrTruncatedInstr), "截断分类:指令不完整")
		checks++
		check(errors.Is(classOf(len(raw)-5), patch.ErrTruncatedData), "截断分类:数据段不完整")
		checks++
		check(errors.Is(classOf(len(raw)-2), patch.ErrCRCMismatch), "截断分类:CRC不匹配")
		checks++
		bad, err := patch.Decode(raw[:len(raw)/2])
		_, errA := patch.Apply(tgt, bad)
		check(err != nil && errA != nil && string(tgt) == string(before), "截断补丁未被部分应用")
		checks++
		pOOR := diff.Diff(src, sg)
		pOOR.Instrs = append(pOOR.Instrs, diff.Instr{Reuse: true, Index: 9999})
		dec, errD := patch.Decode(patch.Encode(pOOR))
		_, errA = patch.Apply(tgt, dec)
		check(errD == nil && errors.Is(errA, patch.ErrBlockOutOfRange), "复用块号越界被检出")
		checks++
	}
	// verify：弱碰撞被强校验挡住且结果正确；签名与数据不符被检出。数据与补丁写临时目录。
	{
		dir, err := os.MkdirTemp("", "osync")
		if err != nil {
			check(false, "创建临时目录")
		}
		defer os.RemoveAll(dir)
		const bs = 4
		tgt := []byte{4, 3, 2, 1, 9, 9, 9, 9} // 首块与源端弱碰撞
		src := []byte{1, 2, 3, 4, 9, 9, 9, 9}
		sg, _ := sig.Generate(tgt, bs)
		p := diff.Diff(src, sg)
		raw := patch.Encode(p)
		os.WriteFile(dir+"/src.bin", src, 0o600)
		os.WriteFile(dir+"/tgt.bin", tgt, 0o600)
		os.WriteFile(dir+"/patch.bin", raw, 0o600)
		rtgt, _ := os.ReadFile(dir + "/tgt.bin")
		rraw, _ := os.ReadFile(dir + "/patch.bin")
		dec, errD := patch.Decode(rraw)
		out, errA := patch.Apply(rtgt, dec)
		errF := verify.Final(out, dec)
		blocked := p.Instrs[0].Reuse == false // 碰撞块未被错误复用
		check(errD == nil && errA == nil && errF == nil && blocked && string(out) == string(src),
			"弱碰撞被强校验挡住且结果正确")
		checks++
		// 目标端在生成签名后改动数据：最终校验必须报错。
		tgt2 := append([]byte(nil), tgt...)
		tgt2[5] ^= 0xFF
		out2, _ := patch.Apply(tgt2, dec)
		check(errors.Is(verify.Final(out2, dec), verify.ErrChecksumMismatch), "签名与数据不符被检出")
		checks++
	}
	fmt.Printf("TOTAL %d checks, %d failures\n", checks, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
