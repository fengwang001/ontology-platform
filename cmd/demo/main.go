// demo 逐条演练 LZ77 压缩器/解压器的语义，全部 OK 时退出码 0。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/dec"
	"ontology/enc"
	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

var (
	ec = enc.Config{WindowCap: 1 << 15, MaxChain: 32}
	dc = dec.Config{WindowCap: 1 << 15}
)

func comp(data []byte) []byte {
	var b bytes.Buffer
	e, _ := enc.New(&b, ec)
	e.Write(data)
	e.Close()
	return b.Bytes()
}

func decomp(s []byte) ([]byte, error) {
	d, _ := dec.New(dc)
	if _, err := d.Write(s); err != nil {
		return d.Output(), err
	}
	return d.Output(), d.Close()
}

func safe(s []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	d, _ := dec.New(dec.Config{WindowCap: 1 << 15, MaxOutput: 1 << 16})
	d.Write(s)
	return d.Output(), d.Close()
}

func examined(data []byte) int {
	w, _ := window.New(1 << 15)
	m, _ := match.New(w, 16)
	src := func(q uint64) byte { return data[q] }
	for p := 0; p+3 <= len(data); p++ {
		m.FindLongest(src, uint64(p), data[p:min(p+256, len(data))])
		w.Append(data[p])
	}
	return m.Examined()
}

func main() {
	ok, total := 0, 0
	check := func(name string, good bool) {
		total++
		if good {
			ok++
			fmt.Println("OK  ", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}
	rnd := make([]byte, 4096)
	rand.New(rand.NewSource(1)).Read(rnd)
	inputs := [][]byte{nil, bytes.Repeat([]byte{9}, 50000), rnd,
		bytes.Repeat([]byte("abc"), 2000), []byte(strings.Repeat("hello world ", 300))}
	good := true
	for _, in := range inputs {
		out, err := decomp(comp(in))
		good = good && err == nil && bytes.Equal(out, in)
	}
	check("roundtrip x5", good)
	good = true
	for _, p := range []int{1, 2, 3} {
		in := bytes.Repeat([]byte("abc")[:p], 3000)
		out, err := decomp(comp(in))
		good = good && err == nil && bytes.Equal(out, in) && len(comp(in)) < 100
	}
	check("overlap backref dist 1/2/3", good)
	data := []byte(strings.Repeat("write-splitting ", 100))
	chunked := func(n int) []byte {
		var b bytes.Buffer
		e, _ := enc.New(&b, ec)
		for i := 0; i < len(data); i += n {
			e.Write(data[i:min(i+n, len(data))])
		}
		e.Close()
		return b.Bytes()
	}
	whole := chunked(len(data))
	check("write split 1/7/whole equal", bytes.Equal(chunked(1), whole) && bytes.Equal(chunked(7), whole))
	var buf bytes.Buffer
	e, _ := enc.New(&buf, ec)
	e.Write(data[:10])
	e.Flush()
	d, _ := dec.New(dc)
	d.Write(buf.Bytes())
	n := buf.Len()
	e.Flush()
	check("flush promise + idle flush empty", bytes.Equal(d.Output(), data[:10]) && buf.Len() == n)
	stream := comp(data)
	good = true
	for cut := 0; cut <= len(stream); cut++ {
		d, _ := dec.New(dc)
		d.Write(stream[:cut])
		d.Write(stream[cut:])
		good = good && d.Close() == nil && bytes.Equal(d.Output(), data)
	}
	check("decode split at every point", good)
	emptyOut, err := decomp(comp(nil))
	_, zerr := decomp(nil)
	check("empty stream vs zero-length", err == nil && len(emptyOut) == 0 && errors.Is(zerr, wire.ErrTruncated))
	h := wire.Header()
	sum := byte(0)
	end := wire.AppendEnd(nil, 2, uint64(sum))
	corrupt := []struct {
		s    []byte
		want error
	}{
		{[]byte{0}, wire.ErrMagic},
		{append(h, wire.AppendBackref(nil, 0, 3)...), wire.ErrDistZero},
		{bytes.Join([][]byte{h, wire.AppendLiteral(nil, []byte("ab")), wire.AppendBackref(nil, 3, 1)}, nil), wire.ErrDistOutput},
		{bytes.Join([][]byte{h, wire.AppendLiteral(nil, []byte("abcd")), wire.AppendBackref(nil, 5, 1)}, nil), wire.ErrDistWindow},
		{append(append(h, bytes.Repeat([]byte{0x80}, 9)...), 2), wire.ErrVarint},
		{bytes.Join([][]byte{h, wire.AppendLiteral(nil, []byte("hi")), wire.AppendEnd(nil, 9, 0)}, nil), wire.ErrLenMismatch},
		{bytes.Join([][]byte{h, wire.AppendLiteral(nil, []byte("hi")), end}, nil), wire.ErrChecksum},
		{append(comp([]byte("hi")), 0), wire.ErrTrailing},
	}
	good = true
	for i, c := range corrupt {
		cfg := dc
		if i == 3 {
			cfg.WindowCap = 4
		}
		dd, _ := dec.New(cfg)
		_, err := dd.Write(c.s)
		good = good && errors.Is(err, c.want)
	}
	check("8 corruption kinds distinct", good)
	bomb := append(append(h, wire.AppendLiteral(nil, []byte("a"))...),
		wire.AppendUvarint(wire.AppendUvarint(nil, uint64(1<<40)<<2|wire.TagBackref), 1)...)
	bd, _ := dec.New(dec.Config{WindowCap: 1 << 15, MaxOutput: 1 << 20})
	_, berr := bd.Write(bomb)
	_, sticky := bd.Write([]byte{0})
	check("bomb rejected before write", errors.Is(berr, wire.ErrOutputLimit) && len(bd.Output()) == 1 && sticky == berr)
	good = true
	for cut := 0; cut < len(stream); cut++ {
		td, _ := dec.New(dc)
		td.Write(stream[:cut])
		good = good && errors.Is(td.Close(), wire.ErrTruncated) && bytes.HasPrefix(data, td.Output())
	}
	check("truncation yields prefix only", good)
	good = true
	for i := range stream {
		for bit := 0; bit < 8; bit++ {
			bad := bytes.Clone(stream)
			bad[i] ^= 1 << bit
			out, err := safe(bad)
			good = good && (err != nil || bytes.Equal(out, data))
		}
	}
	check("bit flips never silently wrong", good)
	base, _ := enc.CompressParallel(data, 256, 1)
	good = true
	for _, w := range []int{2, 4, 8} {
		got, _ := enc.CompressParallel(data, 256, w)
		good = good && bytes.Equal(got, base)
	}
	check("parallel workers 1/2/4/8 equal", good)
	s64, s4m := examined(make([]byte, 64<<10)), examined(make([]byte, 4<<20))
	r1, r2 := float64(s64)/(64<<10), float64(s4m)/(4<<20)
	check("examined per byte scale-free", r1 <= 2*r2 && r2 <= 2*r1)
	fmt.Printf("TOTAL %d/%d\n", ok, total)
	if ok != total {
		os.Exit(1)
	}
}
