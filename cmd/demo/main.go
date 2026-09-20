// demo 逐项验证 framing 包的 8 条语义，逐条打印 OK/FAIL。
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"ontology/framing"
)

func encode(frames ...[]byte) []byte {
	var out []byte
	for _, f := range frames {
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(f)))
		out = append(out, hdr[:]...)
		out = append(out, f...)
	}
	return out
}

func equal(a, b [][]byte) bool {
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

// chunkingInvariance 用多种切法喂同一字节流，要求帧序列逐字节相等。
func chunkingInvariance() bool {
	want := [][]byte{[]byte("hello"), {}, []byte("payload"), {1, 2, 3}}
	stream := encode(want...)
	patterns := [][]int{{len(stream)}, {1}, {2, 3, 1}, {0, 5, 0, 1, 4}}
	for _, sizes := range patterns {
		r := framing.New(64)
		var got [][]byte
		for off, i := 0, 0; off < len(stream); i++ {
			n := sizes[i%len(sizes)]
			if n == 0 {
				r.Feed(nil)
				continue
			}
			if n > len(stream)-off {
				n = len(stream) - off
			}
			f, err := r.Feed(stream[off : off+n])
			if err != nil {
				return false
			}
			got = append(got, f...)
			off += n
		}
		if !equal(got, want) || r.Close() != nil {
			return false
		}
	}
	return true
}

func sticky() bool {
	want := [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")}
	got, err := framing.New(8).Feed(encode(want...))
	return err == nil && equal(got, want)
}

func partial() bool {
	r := framing.New(8)
	s := encode([]byte("half"))
	f1, e1 := r.Feed(s[:3])
	f2, e2 := r.Feed(s[3:])
	return e1 == nil && e2 == nil && len(f1) == 0 && equal(f2, [][]byte{[]byte("half")})
}

func tooLarge() bool {
	r := framing.New(4)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 5)
	_, err := r.Feed(hdr[:])
	if !errors.Is(err, framing.ErrFrameTooLarge) || r.Buffered() != 4 {
		return false
	}
	_, err = r.Feed([]byte("x")) // 终止态：同一错误且 Buffered 不变
	return errors.Is(err, framing.ErrFrameTooLarge) && r.Buffered() == 4
}

func zeroLength() bool {
	got, err := framing.New(4).Feed(encode([]byte{}))
	return err == nil && len(got) == 1 && got[0] != nil && len(got[0]) == 0
}

func copyIsolation() bool {
	r := framing.New(8)
	p := encode([]byte("keep"))
	got, err := r.Feed(p)
	if err != nil || len(got) != 1 {
		return false
	}
	for i := range p {
		p[i] = 0
	}
	got[0][0] = 'X'
	next, err := r.Feed(encode([]byte("ok")))
	return err == nil && string(got[0]) == "Xeep" &&
		string(next[0]) == "ok" && r.Close() == nil
}

func closeSemantics() bool {
	r := framing.New(8)
	r.Feed(encode([]byte("done")))
	if r.Close() != nil || r.Close() != nil {
		return false
	}
	if _, err := r.Feed([]byte("x")); err == nil {
		return false
	}
	for _, n := range []int{1, 2, 3, 6} { // 残留长度字段或半截负载
		r := framing.New(8)
		r.Feed(encode([]byte("abc"))[:n])
		if !errors.Is(r.Close(), framing.ErrIncomplete) ||
			!errors.Is(r.Close(), framing.ErrIncomplete) {
			return false
		}
	}
	return true
}

func boundedGrowth() bool {
	r := framing.New(8)
	chunk := encode([]byte("ab"))
	for i := 0; i < 10000; i++ {
		if _, err := r.Feed(chunk); err != nil {
			return false
		}
	}
	return r.Buffered() == 0
}

func main() {
	checks := []struct {
		name string
		ok   func() bool
	}{
		{"1 chunking invariance", chunkingInvariance},
		{"2 sticky frames", sticky},
		{"3 partial frame", partial},
		{"4 frame too large", tooLarge},
		{"5 zero-length frame", zeroLength},
		{"6 copy isolation", copyIsolation},
		{"7 close semantics", closeSemantics},
		{"8 bounded growth", boundedGrowth},
	}
	failed := 0
	for _, c := range checks {
		verdict := "OK"
		if !c.ok() {
			verdict = "FAIL"
			failed++
		}
		fmt.Printf("%-4s %s\n", verdict, c.name)
	}
	fmt.Printf("summary: %d/%d checks OK\n", len(checks)-failed, len(checks))
}
