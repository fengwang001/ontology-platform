// demo 逐条演练压缩/解压语义并打印 OK/FAIL，退出码 0 表示全部通过。
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

func rnd(n int) []byte { b := make([]byte, n); rand.New(rand.NewSource(42)).Read(b); return b }

func comp(data []byte, chunk int) []byte {
	var buf bytes.Buffer
	c, _ := enc.New(&buf, enc.Config{})
	for i := 0; i < len(data); i += chunk {
		c.Write(data[i:min(i+chunk, len(data))])
	}
	c.Close()
	return buf.Bytes()
}

func decomp(s []byte, maxOut int64) ([]byte, error) {
	d, _ := dec.New(dec.Config{MaxOutput: maxOut})
	if _, err := d.Write(s); err != nil {
		return nil, err
	}
	if err := d.Close(); err != nil {
		return nil, err
	}
	return d.Output(), nil
}

func main() {
	checks := []struct {
		name string
		fn   func() error
	}{
		{"roundtrip x5", func() error {
			for _, d := range [][]byte{{}, bytes.Repeat([]byte("a"), 5000), rnd(4096), bytes.Repeat([]byte("abc"), 1500), bytes.Repeat([]byte("hello ontology "), 300)} {
				if out, err := decomp(comp(d, 7), 0); err != nil || !bytes.Equal(out, d) {
					return fmt.Errorf("len=%d err=%v", len(d), err)
				}
			}
			return nil
		}},
		{"overlap dist 1/2/3", func() error {
			for dist := 1; dist <= 3; dist++ {
				d := bytes.Repeat([]byte("abc")[:dist], 4000)
				if out, _ := decomp(comp(d, 1<<20), 0); !bytes.Equal(out, d) {
					return fmt.Errorf("dist=%d", dist)
				}
			}
			return nil
		}},
		{"write chunking 1/7/all", func() error {
			d := rnd(3000)
			if !bytes.Equal(comp(d, 1), comp(d, 7)) || !bytes.Equal(comp(d, 1), comp(d, 3000)) {
				return errors.New("outputs differ")
			}
			return nil
		}},
		{"flush promise + idle flush", func() error {
			d := rnd(2000)
			var buf bytes.Buffer
			c, _ := enc.New(&buf, enc.Config{})
			c.Write(d[:800])
			c.Flush()
			p, _ := dec.New(dec.Config{})
			if _, err := p.Write(buf.Bytes()); err != nil || !bytes.Equal(p.Output(), d[:800]) {
				return fmt.Errorf("partial: %v", err)
			}
			c.Write(d[800:])
			c.Flush()
			n := buf.Len()
			c.Flush()
			if buf.Len() != n {
				return errors.New("idle flush emitted bytes")
			}
			c.Close()
			out, err := decomp(buf.Bytes(), 0)
			if err != nil || !bytes.Equal(out, d) {
				return err
			}
			return nil
		}},
		{"decode chunking", func() error {
			d, s := rnd(300), comp(rnd(300), 5)
			_ = d
			for i := 0; i <= len(s); i++ {
				p, _ := dec.New(dec.Config{})
				p.Write(s[:i])
				p.Write(s[i:])
				if p.Close() != nil {
					return fmt.Errorf("split %d", i)
				}
			}
			return nil
		}},
		{"empty vs zero-length", func() error {
			s := comp(nil, 1)
			if len(s) == 0 {
				return errors.New("empty stream is empty")
			}
			if out, err := decomp(s, 0); err != nil || len(out) != 0 {
				return fmt.Errorf("empty: %v", err)
			}
			if _, err := decomp(nil, 0); !errors.Is(err, dec.ErrTruncated) {
				return errors.New("zero-length not truncated")
			}
			return nil
		}},
		{"8 corruption classes", checkCorruptions},
		{"bomb rejected early", func() error {
			bomb := bytes.Join([][]byte{wire.Header(), wire.AppendLiteral(nil, []byte("x")), wire.AppendBackref(nil, 1, 1<<40)}, nil)
			d, _ := dec.New(dec.Config{MaxOutput: 100})
			if _, err := d.Write(bomb); !errors.Is(err, dec.ErrOutputLimit) {
				return fmt.Errorf("got %v", err)
			}
			if !bytes.Equal(d.Output(), []byte("x")) {
				return errors.New("output not preserved")
			}
			return nil
		}},
		{"truncation walk", func() error {
			d, s := rnd(200), comp(rnd(200), 3)
			for i := 0; i < len(s); i++ {
				p, _ := dec.New(dec.Config{})
				p.Write(s[:i])
				if !errors.Is(p.Close(), dec.ErrTruncated) || !bytes.Equal(p.Output(), d[:len(p.Output())]) {
					return fmt.Errorf("at %d", i)
				}
			}
			return nil
		}},
		{"bit flip walk", func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			d, s := rnd(150), comp(rnd(150), 3)
			for i := range s {
				bad := bytes.Clone(s)
				bad[i] ^= 1 << uint(i%8)
				if out, e := decomp(bad, int64(len(d)+100)); e == nil && !bytes.Equal(out, d) {
					return fmt.Errorf("silent corruption at %d", i)
				}
			}
			return nil
		}},
		{"parallel workers 1/2/4/8", func() error {
			d := append(bytes.Repeat([]byte("ontology-"), 10000), rnd(1<<18)...)[:1<<18]
			ref, _ := enc.CompressParallel(d, 16384, 1)
			for _, w := range []int{2, 4, 8} {
				if got, _ := enc.CompressParallel(d, 16384, w); !bytes.Equal(ref, got) {
					return fmt.Errorf("workers=%d", w)
				}
			}
			out, err := decomp(ref, 0)
			if err != nil || !bytes.Equal(out, d) {
				return err
			}
			return nil
		}},
		{"candidate counts 64KB vs 4MB", func() error {
			ratio := perByte(64<<10) / perByte(4<<20)
			if ratio > 2 || ratio < 0.5 {
				return fmt.Errorf("ratio %.2f", ratio)
			}
			if len(comp(bytes.Repeat([]byte{7}, 4<<20), 1<<22)) >= 64<<10 {
				return errors.New("4MB not compressed below 64KB")
			}
			return nil
		}},
	}
	ok := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
		} else {
			fmt.Printf("OK   %s\n", c.name)
			ok++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", ok, len(checks))
	if ok != len(checks) {
		os.Exit(1)
	}
}

func checkCorruptions() error {
	valid := comp(rnd(200), 1<<20)
	sum := wire.Checksum([]byte("ab"))
	lit := wire.AppendLiteral(nil, []byte("ab"))
	bad := bytes.Clone(valid)
	bad[0] ^= 0xFF
	ver := bytes.Clone(valid)
	ver[2] = 9
	cases := []struct {
		s    []byte
		want error
	}{
		{bad, dec.ErrBadMagic}, {ver, dec.ErrBadVersion},
		{append(wire.Header(), wire.AppendBackref(nil, 0, 4)...), dec.ErrDistZero},
		{bytes.Join([][]byte{wire.Header(), lit, wire.AppendBackref(nil, 3, 2)}, nil), dec.ErrDistBeyondOutput},
		{append(wire.Header(), 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF), dec.ErrVarintTooLong},
		{bytes.Join([][]byte{wire.Header(), lit, wire.AppendTrailer(nil, 3, sum)}, nil), dec.ErrLengthMismatch},
		{bytes.Join([][]byte{wire.Header(), lit, wire.AppendTrailer(nil, 2, sum+1)}, nil), dec.ErrChecksumMismatch},
		{append(bytes.Clone(valid), 0), dec.ErrTrailingData},
	}
	for i, c := range cases {
		if _, err := decomp(c.s, 0); !errors.Is(err, c.want) {
			return fmt.Errorf("case %d: %v", i, err)
		}
	}
	return nil
}

func perByte(n int) float64 {
	win, _ := window.New(1 << 16)
	m, _ := match.New(win, 32)
	m.Append(bytes.Repeat([]byte{0x5A}, n))
	for pos := 0; pos < n; {
		if _, l := m.Find(pos); l >= match.MinLen {
			pos += l
		} else {
			pos++
		}
	}
	return float64(m.Candidates()) / float64(n)
}
