package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/esc"
	"ontology/frame"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	word := "OK"
	if !ok {
		word = "FAIL"
	}
	fmt.Println(name, word)
}

func equalFrames(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func main() {
	a := api.New()
	check("api self-check", a.SelfCheck() == nil)

	// 第三节：三个帧各自的转义字节
	escOK := bytes.Equal(esc.Escape([]byte("hi")), []byte{0x68, 0x69}) &&
		bytes.Equal(esc.Escape([]byte("a\nb")), []byte{0x61, 0x5C, 0x6E, 0x62}) &&
		bytes.Equal(esc.Escape([]byte(`x\y`)), []byte{0x78, 0x5C, 0x5C, 0x79})
	check("escape bytes of 3 frames", escOK)

	// 第三节：整条流
	trio := [][]byte{[]byte("hi"), []byte("a\nb"), []byte(`x\y`)}
	wantStream := []byte{0x68, 0x69, 0x0A, 0x61, 0x5C, 0x6E, 0x62, 0x0A, 0x78, 0x5C, 0x5C, 0x79, 0x0A}
	check("stream bytes", bytes.Equal(a.EncodeFrames(trio), wantStream))

	// 往返一致 + 空帧合法
	frames := [][]byte{nil, {}, []byte("a\nb"), []byte(`x\y`), []byte("plain")}
	back, err := a.DecodeFrames(a.EncodeFrames(frames))
	check("round trip incl empty frames", err == nil && equalFrames(back, frames))

	// 故障注入：非法转义 / 悬空转义 / 末尾无分隔符
	_, err1 := a.DecodeFrames([]byte("a\\xb\n"))
	_, err2 := a.DecodeFrames([]byte("ab\\\n"))
	_, err3 := a.DecodeFrames([]byte("ab"))
	check("invalid escape rejected", errors.Is(err1, esc.ErrInvalidEscape))
	check("dangling escape rejected", errors.Is(err2, esc.ErrDanglingEscape))
	check("missing terminator rejected", errors.Is(err3, frame.ErrMissingTerminator))

	// 被拒后游标不变
	r := frame.NewReader([]byte("ok\n\\x\n"))
	_, _ = r.NextFrame()
	_, err = r.NextFrame()
	check("cursor unchanged on error", err != nil && r.Pos() == 3)

	// 大 m 单趟解码（零回看由 frame 包内测试钉住）
	rng := rand.New(rand.NewSource(7))
	big := make([][]byte, 10000)
	for i := range big {
		p := make([]byte, rng.Intn(32))
		rng.Read(p)
		big[i] = p
	}
	gotBig, err := a.DecodeFrames(a.EncodeFrames(big))
	check("large m single-pass decode", err == nil && equalFrames(gotBig, big))

	// 并发编解码一致
	shared := a.EncodeFrames(trio)
	var wg sync.WaitGroup
	concOK := true
	for g := 0; g < 8; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			got, err := a.DecodeFrames(shared)
			if err != nil || !equalFrames(got, trio) {
				concOK = false
			}
		}()
		go func(g int) {
			defer wg.Done()
			if !bytes.Equal(a.EncodeFrames(big[g*1000:(g+1)*1000]), frame.Encode(big[g*1000:(g+1)*1000])) {
				concOK = false
			}
		}(g)
	}
	wg.Wait()
	check("concurrent encode/decode", concOK)

	if failed {
		os.Exit(1)
	}
}
