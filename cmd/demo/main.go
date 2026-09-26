package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/codec"
	"ontology/ord"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	buf := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	check("六组合解码", ord.LittleEndian.Uint16(buf) == 0x0201 &&
		ord.BigEndian.Uint16(buf) == 0x0102 &&
		ord.LittleEndian.Uint32(buf) == 0x04030201 &&
		ord.BigEndian.Uint32(buf) == 0x01020304 &&
		ord.LittleEndian.Uint64(buf) == 0x0807060504030201 &&
		ord.BigEndian.Uint64(buf) == 0x0102030405060708)
	check("Swap两次还原", ord.Swap16(ord.Swap16(0x1234)) == 0x1234 &&
		ord.Swap32(ord.Swap32(0x12345678)) == 0x12345678 &&
		ord.Swap64(ord.Swap64(0x0102030405060708)) == 0x0102030405060708)
	check("BE/LE互为字节反转", ord.LittleEndian.Uint32(buf) == ord.Swap32(ord.BigEndian.Uint32(buf)))
	check("FF..FF 有/无符号", ord.BigEndian.Uint32([]byte{0xFF, 0xFF, 0xFF, 0xFF}) == 4294967295 &&
		ord.BigEndian.Int32([]byte{0xFF, 0xFF, 0xFF, 0xFF}) == -1)
	fit := map[int][]int64{
		2: {-1, 0, 1, 32767, -32768},
		4: {-1, 1<<31 - 1, -1 << 31},
		8: {-1, 1<<63 - 1, -1 << 63},
	}
	round := true
	for _, w := range []int{2, 4, 8} {
		for _, o := range []ord.ByteOrder{ord.BigEndian, ord.LittleEndian} {
			back, err := codec.Decode(o, w, codec.Encode(o, w, fit[w]))
			if err != nil || len(back) != len(fit[w]) {
				round = false
				continue
			}
			for i, v := range fit[w] {
				if back[i] != v {
					round = false
				}
			}
		}
	}
	check("往返一致", round)
	_, errW := codec.Decode(ord.BigEndian, 3, make([]byte, 6))
	check("宽度非法被拒", errW == codec.ErrInvalidWidth)
	_, errL := codec.Decode(ord.BigEndian, 4, make([]byte, 6))
	check("长度不对齐被拒", errL == codec.ErrMisaligned)
	const m = 10000
	big := make([]int64, m)
	for i := range big {
		big[i] = int64(i)*2654435761 - 1<<40
	}
	dec, _ := codec.NewDecoder(ord.LittleEndian, 8, codec.Encode(ord.LittleEndian, 8, big))
	zero := true
	for i := 0; i < m; i++ {
		v, ok := dec.Next()
		if !ok || v != big[i] {
			zero = false
		}
	}
	check("大m游标解码零回看", zero)
	check("SelfCheck", api.New().SelfCheck() == nil)
	conc := true
	var wg sync.WaitGroup
	shared := codec.Encode(ord.BigEndian, 4, fit[4])
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			got, err := codec.Decode(ord.BigEndian, 4, shared)
			if err != nil || len(got) != len(fit[4]) {
				conc = false
				return
			}
			for j, v := range fit[4] {
				if got[j] != v {
					conc = false
				}
			}
			mine := codec.Encode(ord.LittleEndian, 8, []int64{int64(seed), -int64(seed)})
			back, err := codec.Decode(ord.LittleEndian, 8, mine)
			if err != nil || back[0] != int64(seed) || back[1] != -int64(seed) {
				conc = false
			}
		}(i)
	}
	wg.Wait()
	check("并发编解码一致", conc)
	if fails > 0 {
		os.Exit(1)
	}
}
