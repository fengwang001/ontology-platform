package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/codec"
	"ontology/ord"
)

var failed bool

func report(ok bool, what string) {
	if ok {
		fmt.Println("OK  " + what)
	} else {
		fmt.Println("FAIL " + what)
		failed = true
	}
}

func main() {
	buf := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	// 第三节：六个宽度/序组合的解码值
	six := ord.Uint16(ord.LittleEndian, buf) == 0x0201 &&
		ord.Uint16(ord.BigEndian, buf) == 0x0102 &&
		ord.Uint32(ord.LittleEndian, buf) == 0x04030201 &&
		ord.Uint32(ord.BigEndian, buf) == 0x01020304 &&
		ord.Uint64(ord.LittleEndian, buf) == 0x0807060504030201 &&
		ord.Uint64(ord.BigEndian, buf) == 0x0102030405060708
	report(six, "six width/order combos decode to table values")

	// Swap 两次还原
	swap2 := true
	for _, v := range []uint64{0, 1, 0x0102030405060708, 0xDEADBEEFCAFEBABE, 0xFFFFFFFFFFFFFFFF} {
		if ord.Swap16(ord.Swap16(uint16(v))) != uint16(v) ||
			ord.Swap32(ord.Swap32(uint32(v))) != uint32(v) ||
			ord.Swap64(ord.Swap64(v)) != v {
			swap2 = false
		}
	}
	report(swap2, "Swap(Swap(v)) == v for 16/32/64")

	// BE 写、LE 读同一字节串 = 字节反转
	cross := true
	var b8 [8]byte
	for _, v := range []uint64{0x0102030405060708, 0x8000000000000001} {
		ord.PutUint64(ord.BigEndian, b8[:], v)
		cross = cross && ord.Uint64(ord.LittleEndian, b8[:]) == ord.Swap64(v)
		ord.PutUint32(ord.LittleEndian, b8[:4], uint32(v))
		cross = cross && ord.Uint32(ord.BigEndian, b8[:4]) == ord.Swap32(uint32(v))
	}
	report(cross, "BE-write/LE-read equals byte-reversed value")

	// 往返一致：全部宽度 × 字节序，含随机值
	rt := true
	rng := rand.New(rand.NewSource(694))
	for _, o := range []ord.ByteOrder{ord.BigEndian, ord.LittleEndian} {
		for _, w := range []int{2, 4, 8} {
			vals := make([]int64, 64)
			for i := range vals {
				vals[i] = int64(int64(rng.Uint64()) << (64 - 8*w) >> (64 - 8*w)) // 截到 w 字节有符号范围
			}
			got, err := codec.Decode(o, w, codec.Encode(o, w, vals))
			if err != nil || len(got) != len(vals) {
				rt = false
				continue
			}
			for i := range vals {
				rt = rt && got[i] == vals[i]
			}
		}
	}
	report(rt, "Decode(Encode(vals)) == vals for all widths/orders")

	// 宽度非法被拒
	_, errW := codec.Decode(ord.BigEndian, 3, []byte{1, 2, 3})
	report(errors.Is(errW, codec.ErrWidth) && codec.Encode(ord.BigEndian, 3, []int64{1}) == nil,
		"invalid width rejected with ErrWidth")

	// 长度不对齐被拒，且与宽度非法错误不同
	_, errA := codec.Decode(ord.LittleEndian, 4, []byte{1, 2, 3})
	report(errors.Is(errA, codec.ErrAlign) && !errors.Is(errA, codec.ErrWidth),
		"misaligned buffer rejected with ErrAlign")

	// FF FF FF FF：无符号 vs 有符号
	ff := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	report(ord.Uint32(ord.BigEndian, ff) == 4294967295 && ord.Int32(ord.BigEndian, ff) == -1,
		"FF FF FF FF: uint32=4294967295 int32=-1")

	// 大 m 逐整数解码（零回看的严格断言见 codec 包内测试）
	const m = 10000
	vals := make([]int64, m)
	for i := range vals {
		vals[i] = int64(i)*2654435761 + math.MinInt64
	}
	got, err := codec.Decode(ord.LittleEndian, 8, codec.Encode(ord.LittleEndian, 8, vals))
	large := err == nil && len(got) == m
	for i := range vals {
		large = large && got[i] == vals[i]
	}
	report(large, "m=10000 uint64 cursor decode, direct-offset (zero rescan)")

	// api 自检：四条不变量的内置向量核验
	report(api.New().SelfCheck() == nil, "api.SelfCheck passes all four invariants")

	// 并发：N 路并发 Decode 同一段只读字节结果一致；N 路并发 Encode 与串行一致
	const n = 16
	shared := codec.Encode(ord.BigEndian, 4, vals[:64])
	want, _ := codec.Decode(ord.BigEndian, 4, shared)
	encWant := make([][]byte, n)
	for g := 0; g < n; g++ {
		encWant[g] = codec.Encode(ord.LittleEndian, 8, vals[g*8:(g+1)*8])
	}
	var wg sync.WaitGroup
	conc := true
	for g := 0; g < n; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			got, err := codec.Decode(ord.BigEndian, 4, shared)
			if err != nil || len(got) != len(want) {
				conc = false
				return
			}
			for i := range want {
				conc = conc && got[i] == want[i]
			}
		}()
		go func(g int) {
			defer wg.Done()
			if !bytes.Equal(codec.Encode(ord.LittleEndian, 8, vals[g*8:(g+1)*8]), encWant[g]) {
				conc = false
			}
		}(g)
	}
	wg.Wait()
	report(conc, "concurrent Decode/Encode consistent with serial results")

	if failed {
		os.Exit(1)
	}
}
