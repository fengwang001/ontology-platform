// demo 逐条演练压缩器/解压器的关键语义，每步打印 OK/FAIL。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"

	"ontology/dec"
	"ontology/enc"
	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

var cfg = enc.Config{Window: 1 << 16, Chain: 128}

func compress(data []byte, chunk int) []byte {
	var buf bytes.Buffer
	e, _ := enc.New(&buf, cfg)
	for i := 0; i < len(data); i += chunk {
		e.Write(data[i:min(i+chunk, len(data))])
	}
	e.Close()
	return buf.Bytes()
}

func decode(s []byte, max uint64) ([]byte, error) {
	d, _ := dec.New(max, 1<<16)
	if _, err := d.Write(s); err != nil {
		return d.Output(), err
	}
	return d.Output(), d.Close()
}

func check(name string, fn func() error) bool {
	if err := fn(); err != nil {
		fmt.Printf("FAIL %-22s %v\n", name, err)
		return false
	}
	fmt.Printf("OK   %s\n", name)
	return true
}

func main() {
	rnd := rand.New(rand.NewSource(1))
	r := make([]byte, 50000)
	rnd.Read(r)
	inputs := [][]byte{nil, bytes.Repeat([]byte{7}, 50000), r,
		bytes.Repeat([]byte("abc"), 20000), bytes.Repeat([]byte("hello "), 8000)}
	n := 0
	ok := func(b bool) {
		if b {
			n++
		}
	}
	ok(check("roundtrip-5-kinds", func() error {
		for i, d := range inputs {
			out, err := decode(compress(d, 1<<20), 0)
			if err != nil || !bytes.Equal(out, d) {
				return fmt.Errorf("input %d: %v", i, err)
			}
		}
		return nil
	}))
	ok(check("overlap-dist-1-2-3", func() error {
		for p := 1; p <= 3; p++ {
			d := bytes.Repeat([]byte("abcdefg"[:p]), 30000)
			if out, _ := decode(compress(d, 1<<20), 0); !bytes.Equal(out, d) {
				return fmt.Errorf("dist %d", p)
			}
		}
		return nil
	}))
	ok(check("write-chunking-same", func() error {
		d := inputs[4]
		a := compress(d, 1)
		if !bytes.Equal(a, compress(d, 7)) || !bytes.Equal(a, compress(d, len(d))) {
			return errors.New("streams differ")
		}
		return nil
	}))
	ok(check("flush-promise", func() error {
		var buf bytes.Buffer
		e, _ := enc.New(&buf, cfg)
		e.Write([]byte("part1 "))
		e.Flush()
		d, _ := dec.New(0, 1<<16)
		d.Write(buf.Bytes())
		if !bytes.Equal(d.Output(), []byte("part1 ")) {
			return errors.New("flush did not cover input")
		}
		n := buf.Len()
		e.Flush()
		if buf.Len() != n {
			return errors.New("empty flush emitted bytes")
		}
		e.Write([]byte("part2"))
		return e.Close()
	}))
	ok(check("decode-chunking-same", func() error {
		s := compress(inputs[3], 1<<20)
		for cut := 0; cut <= len(s); cut++ {
			d, _ := dec.New(0, 1<<16)
			d.Write(s[:cut])
			d.Write(s[cut:])
			if d.Close() != nil || !bytes.Equal(d.Output(), inputs[3]) {
				return fmt.Errorf("cut %d", cut)
			}
		}
		return nil
	}))
	ok(check("empty-vs-zero-stream", func() error {
		if out, err := decode(compress(nil, 1), 0); err != nil || len(out) != 0 {
			return errors.New("empty input not a legal stream")
		}
		if _, err := decode(nil, 0); !errors.Is(err, dec.ErrTruncated) {
			return errors.New("zero-length stream not truncated")
		}
		return nil
	}))
	ok(check("corruption-8-kinds", func() error {
		good := compress([]byte("abcabc"), 1<<20)
		cases := []struct {
			s    []byte
			want error
		}{
			{append([]byte{0}, good[1:]...), dec.ErrMagic},
			{append(hdr()[:3], append([]byte{9}, good[4:]...)...), dec.ErrVersion},
			{append(hdr(), wire.AppendRef(nil, 0, 1)...), dec.ErrDistZero},
			{append(append(hdr(), wire.AppendLiteral(nil, []byte("ab"))...), wire.AppendRef(nil, 9, 1)...), dec.ErrDistTooFar},
			{append(hdr(), wire.AppendRef(nil, 1, 1)...), dec.ErrDistTooFar},
			{append(append(hdr(), wire.TagLiteral), bytes.Repeat([]byte{0x80}, 11)...), dec.ErrVarint},
			{append(append(hdr(), wire.AppendLiteral(nil, []byte("ab"))...), wire.AppendEnd(nil, 5, 0)...), dec.ErrLenMismatch},
			{append(good, 0), dec.ErrTrailing},
		}
		for i, c := range cases {
			if _, err := decode(c.s, 0); !errors.Is(err, c.want) {
				return fmt.Errorf("case %d: got %v", i, err)
			}
		}
		return nil
	}))
	ok(check("bomb-rejected-early", func() error {
		s := append(append(hdr(), wire.AppendLiteral(nil, []byte("a"))...), wire.AppendRef(nil, 1, 1<<40)...)
		out, err := decode(s, 1000)
		if !errors.Is(err, dec.ErrTooBig) || len(out) != 1 {
			return fmt.Errorf("err=%v out=%d", err, len(out))
		}
		return nil
	}))
	ok(check("truncation-prefix-only", func() error {
		orig := []byte("hello hello hello hello")
		s := compress(orig, 1<<20)
		for cut := 0; cut < len(s); cut++ {
			d, _ := dec.New(0, 1<<16)
			d.Write(s[:cut])
			if !errors.Is(d.Close(), dec.ErrTruncated) || !bytes.Equal(d.Output(), orig[:len(d.Output())]) {
				return fmt.Errorf("cut %d", cut)
			}
		}
		return nil
	}))
	ok(check("bitflip-no-panic", func() error {
		orig := []byte("flip me flip me!")
		s := compress(orig, 1<<20)
		for i := range s {
			bad := append([]byte{}, s...)
			bad[i] ^= 0x40
			out, err := decode(bad, uint64(len(orig)))
			if err == nil && !bytes.Equal(out, orig) {
				return fmt.Errorf("silent corruption at %d", i)
			}
		}
		return nil
	}))
	ok(check("parallel-deterministic", func() error {
		d := append(bytes.Repeat([]byte("abc"), 30000), r...)
		base, _ := enc.CompressParallel(d, 4096, 1)
		for _, w := range []int{2, 4, 8} {
			got, _ := enc.CompressParallel(d, 4096, w)
			if !bytes.Equal(got, base) {
				return fmt.Errorf("workers=%d", w)
			}
		}
		out, err := decode(base, 0)
		if err != nil || !bytes.Equal(out, d) {
			return err
		}
		return nil
	}))
	ok(check("candidates-two-scales", func() error {
		count := func(n int) int64 {
			win, _ := window.New(1 << 16)
			m, _ := match.New(win, 128)
			d := make([]byte, n)
			m.SetData(d)
			for p := 0; p < n; {
				_, l := m.Longest(p)
				if l == 0 {
					l = 1
				}
				for k := p; k < p+l; k++ {
					m.Advance(k)
				}
				p += l
			}
			return m.Candidates()
		}
		a, b := count(64<<10), count(4<<20)
		pa, pb := float64(a)/(64<<10), float64(b)/(4<<20)
		if pa > 2*pb || pb > 2*pa {
			return fmt.Errorf("per-byte %g vs %g", pa, pb)
		}
		if len(compress(make([]byte, 4<<20), 1<<20)) >= 64<<10 {
			return errors.New("4MB same-byte not below 64KB")
		}
		return nil
	}))
	fmt.Printf("TOTAL %d/12 OK\n", n)
	if n != 12 {
		os.Exit(1)
	}
}

func hdr() []byte { return wire.AppendHeader(nil) }
