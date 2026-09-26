// Command demo 逐条打印 Base64 实现的判定结果，全部 OK 时退出码为 0。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/b64"
	"ontology/codec"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

// sextets 把一块（1..3 字节）切成 6 位值（十进制），不足的不列。
func sextets(b []byte) []int {
	var v [3]byte
	copy(v[:], b)
	n := uint32(v[0])<<16 | uint32(v[1])<<8 | uint32(v[2])
	out := []int{int(n >> 18 & 63), int(n >> 12 & 63)}
	if len(b) > 1 {
		out = append(out, int(n>>6&63))
	}
	if len(b) > 2 {
		out = append(out, int(n&63))
	}
	return out
}

func main() {
	// 第三节：三块各自的 6 位值与字符、完整编码串。
	src := []byte("foobarb")
	enc := b64.Encode(src)
	got := fmt.Sprintf("%v=%s %v=%s %v=%s",
		sextets(src[0:3]), enc[0:4], sextets(src[3:6]), enc[4:8], sextets(src[6:7]), enc[8:12])
	ok("blocks "+got, got == "[25 38 61 47]=Zm9v [24 38 5 50]=YmFy [24 32]=Yg==")
	ok("encode foobarb = "+string(enc), string(enc) == "Zm9vYmFyYg==")

	// 往返一致：覆盖 0..8 及更长的确定性伪随机输入。
	round := true
	for n := 0; n <= 300; n++ {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*131 + n*17)
		}
		d, err := b64.Decode(b64.Encode(b))
		if err != nil || !bytes.Equal(d, b) {
			round = false
		}
	}
	ok("roundtrip len 0..300", round)

	// 1/2/3 字节填充正确。
	pad := string(b64.Encode([]byte("f"))) == "Zg==" &&
		string(b64.Encode([]byte("fo"))) == "Zm8=" &&
		string(b64.Encode([]byte("foo"))) == "Zm9v"
	ok("padding 1/2/3 bytes", pad)

	// 故障注入：四类非法输入各自被拒且不返回部分结果。
	r1, e1 := b64.Decode([]byte("Zm9="))
	ok("Zm9= trailing-bits rejected", errors.Is(e1, b64.ErrTrailingBits) && r1 == nil)
	r2, e2 := b64.Decode([]byte("Zm$v"))
	ok("invalid char rejected", errors.Is(e2, b64.ErrInvalidChar) && r2 == nil)
	r3, e3 := b64.Decode([]byte("TWE"))
	ok("length-not-mult-4 rejected", errors.Is(e3, b64.ErrLength) && r3 == nil)
	r4, e4 := b64.Decode([]byte("Zm=v"))
	ok("'=': position rejected", errors.Is(e4, b64.ErrPadding) && r4 == nil)

	// 大 n 下定长块随机访问（O(1) 恒读 4 字节由 codec 包内测试钉住）。
	n := 10000
	raw := make([]byte, 3*n)
	for i := range raw {
		raw[i] = byte(i * 31)
	}
	buf := b64.Encode(raw)
	blk := codec.BlockCount(buf) == n
	for _, k := range []int{1, n - 1} {
		got, err := codec.DecodeBlockAt(buf, k)
		want, _ := b64.Decode(buf[4*k : 4*k+4])
		blk = blk && err == nil && bytes.Equal(got, want)
	}
	ok("DecodeBlockAt big-n O(1) blocks", blk)

	// 并发编解码一致 + api 自检。
	a := api.New()
	ok("concurrent encode/decode + SelfCheck", a.SelfCheck() == nil && concOK(a))

	if failed {
		os.Exit(1)
	}
}

// concOK：N 个 goroutine 并发解同一段已编码字节，另 N 个并发编各自输入。
func concOK(a *api.API) bool {
	const n = 64
	const shared = "shared payload 0123456789"
	enc := a.EncodeString(shared)
	dec := make([]string, n)
	outs := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if s, err := a.DecodeString(enc); err == nil {
				dec[i] = s
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			outs[i] = a.EncodeString(fmt.Sprintf("payload-%d", i))
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if dec[i] != shared {
			return false
		}
		if outs[i] != a.EncodeString(fmt.Sprintf("payload-%d", i)) {
			return false
		}
	}
	return true
}
