// Command demo 对 Elias delta 编码实现做一组可判定的自检，逐条打印 OK/FAIL。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/bits"
	"ontology/dcode"
)

var ok = true

func check(name string, pass bool) {
	if pass {
		fmt.Println("OK  ", name)
	} else {
		ok = false
		fmt.Println("FAIL", name)
	}
}

func main() {
	// 1. 第三节五个 delta 码与最终字节。
	enc, _ := api.Encode([]int64{1, 2, 4, 10, 16})
	check("delta码 1/0100/01100/00100010/001010000 -> A3 08 8A 00",
		bytes.Equal(enc, []byte{0xA3, 0x08, 0x8A, 0x00}))

	// 2. 忘丢最高位 1 的错码 011100，被正确解码器解得 6 而非 4。
	w := bits.NewWriter()
	w.WriteBits(0b011, 3) // gamma(3)
	w.WriteBits(0b100, 3) // 错：binary(4) 全位
	bad, _ := dcode.DecodeOne(bits.NewReader(w.Bytes()))
	check("错码 011100 被正确解码器解得 6（而非 4）", bad == 6)

	// 3. delta(1) 的单字节与空输入。
	one, _ := api.Encode([]int64{1})
	empty, _ := api.Encode(nil)
	check("Encode([1])=0x80 且 Encode([]) 为空", len(one) == 1 && one[0] == 0x80 && len(empty) == 0)

	// 4. L=64 的码（gamma(64) 后接 63 位）必须报 ErrOverflow。
	w2 := bits.NewWriter()
	w2.WriteBits(0, 6)  // 6 个 0
	w2.WriteBits(64, 7) // binary(64)
	w2.WriteBits(0, 63) // 63 位值层
	_, err := dcode.DecodeOne(bits.NewReader(w2.Bytes()))
	check("L=64 的码报 ErrOverflow", errors.Is(err, api.ErrOverflow))

	// 5. 往返一致（含大数与多组序列）。
	rt := true
	for _, vs := range [][]int64{{1}, {1, 2, 4, 10, 16}, {5, 1 << 40, 9, 1 << 62, 3}} {
		b, _ := api.Encode(vs)
		back, e := api.Decode(b)
		if e != nil || fmt.Sprint(back) != fmt.Sprint(vs) {
			rt = false
		}
	}
	check("Decode(Encode(vs)) == vs 往返一致", rt)

	// 6. 切分点无关：每个切点两刀切 + 逐字节喂，结果与一次性 Decode 相同。
	ci := true
	want, _ := api.Decode(enc)
	for i := 0; i <= len(enc); i++ {
		sd := dcode.NewStreamDecoder()
		sd.Feed(enc[:i])
		sd.Feed(enc[i:])
		got, e := sd.Decode()
		if e != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			ci = false
		}
	}
	check("任意切分点 Feed 与一次性 Decode 一致", ci)

	// 7. 三类错误可判定且互不相同。
	_, e1 := api.Encode([]int64{0})
	_, e2 := api.Decode(enc[:3]) // 末码被截断
	e3 := err
	check("ErrNonPositive/ErrTruncated/ErrOverflow 可 errors.Is 区分",
		errors.Is(e1, api.ErrNonPositive) && errors.Is(e2, api.ErrTruncated) &&
			errors.Is(e3, api.ErrOverflow) &&
			!errors.Is(e1, api.ErrTruncated) && !errors.Is(e2, api.ErrOverflow))

	// 8. 被拒后无部分输出，且解码器可继续使用。
	part, _ := api.Decode(enc[:3])
	sd := dcode.NewStreamDecoder()
	sd.Feed(enc[:3])
	if _, e := sd.Decode(); e == nil {
		ok = false
	}
	sd2 := dcode.NewStreamDecoder()
	sd2.Feed(enc)
	again, e := sd2.Decode()
	check("失败返回 nil 无部分输出，拒绝后可继续使用", part == nil && e == nil && fmt.Sprint(again) == fmt.Sprint(want))

	// 9. 前 m 个 1 后接 2^40：解码大整数检查的位数不随 m 增长。
	const wantBits = 11 + 40 // gamma(41) 11 位 + 值层 40 位
	indep := true
	for _, m := range []int{100, 1000, 10000} {
		vs := make([]int64, m+1)
		for i := range vs {
			vs[i] = 1
		}
		vs[m] = 1 << 40
		b, _ := api.Encode(vs)
		r := bits.NewReader(b)
		var last uint64
		for i := 0; i <= m; i++ {
			p0 := r.Pos()
			if _, e := dcode.DecodeOne(r); e != nil {
				indep = false
			}
			last = r.Pos() - p0
		}
		if last != wantBits {
			indep = false
		}
	}
	check("解码 2^40 检查位数恒为 51，不随 m 增长", indep)

	// 10. 并发：N 个 goroutine 各自 Encode+Decode，与串行结果逐字节/逐元素相同。
	in := []int64{7, 1 << 30, 42, 1, 1 << 62}
	serial, _ := api.Encode(in)
	serialDec, _ := api.Decode(serial)
	var wg sync.WaitGroup
	raceOK := true
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := 0; it < 50; it++ {
				b, _ := api.Encode(in)
				d, _ := api.Decode(b)
				if !bytes.Equal(b, serial) || fmt.Sprint(d) != fmt.Sprint(serialDec) {
					raceOK = false
				}
			}
		}()
	}
	wg.Wait()
	check("8 goroutine 并发编解码与串行结果一致", raceOK)

	if !ok {
		os.Exit(1)
	}
}
