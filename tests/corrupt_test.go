package tests

import (
	"bytes"
	"errors"
	"testing"

	"ontology/dec"
	"ontology/wire"
)

func craft(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func lit(p ...byte) []byte { return wire.AppendLiteral(nil, p) }

func TestCorruption(t *testing.T) {
	data := randBytes(300, 4)
	valid := compress(t, data, 1<<20)
	badMagic := bytes.Clone(valid)
	badMagic[0] ^= 0xFF
	badVer := bytes.Clone(valid)
	badVer[2] = 0x7F
	sum := wire.Checksum([]byte("ab"))
	cases := []struct {
		name   string
		stream []byte
		window int
		want   error
	}{
		{"badMagic", badMagic, 0, dec.ErrBadMagic},
		{"badVersion", badVer, 0, dec.ErrBadVersion},
		{"distZero", craft(wire.Header(), wire.AppendBackref(nil, 0, 4)), 0, dec.ErrDistZero},
		{"distBeyondOutput", craft(wire.Header(), lit('a', 'b'), wire.AppendBackref(nil, 3, 2)), 0, dec.ErrDistBeyondOutput},
		{"distBeyondWindow", craft(wire.Header(), lit('a', 'b', 'c', 'd'), wire.AppendBackref(nil, 5, 1)), 4, dec.ErrDistBeyondWindow},
		{"varintTooLong", craft(wire.Header(), bytes.Repeat([]byte{0xFF}, 11)), 0, dec.ErrVarintTooLong},
		{"lengthMismatch", craft(wire.Header(), lit('a', 'b'), wire.AppendTrailer(nil, 3, sum)), 0, dec.ErrLengthMismatch},
		{"checksumMismatch", craft(wire.Header(), lit('a', 'b'), wire.AppendTrailer(nil, 2, sum+1)), 0, dec.ErrChecksumMismatch},
		{"trailingData", append(bytes.Clone(valid), 0), 0, dec.ErrTrailingData},
	}
	for _, tc := range cases {
		d, err := dec.New(dec.Config{Window: tc.window})
		if err != nil {
			t.Fatal(err)
		}
		_, werr := d.Write(tc.stream)
		if werr == nil {
			werr = d.Close()
		}
		if !errors.Is(werr, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, werr, tc.want)
		}
	}
}

func TestTruncationWalk(t *testing.T) {
	data := randBytes(400, 5)
	stream := compress(t, data, 1<<20)
	for i := 0; i < len(stream); i++ {
		d, _ := dec.New(dec.Config{Window: testWindow})
		d.Write(stream[:i])
		if err := d.Close(); !errors.Is(err, dec.ErrTruncated) {
			t.Fatalf("截断到 %d: got %v", i, err)
		}
		if out := d.Output(); !bytes.Equal(out, data[:len(out)]) {
			t.Fatalf("截断到 %d: 输出不是原文前缀", i)
		}
	}
}

func TestBitFlipWalk(t *testing.T) {
	data := randBytes(200, 6)
	stream := compress(t, data, 1<<20)
	maxOut := int64(len(data) + 100)
	for i := range stream {
		bad := bytes.Clone(stream)
		bad[i] ^= 1 << uint(i%8)
		d, _ := dec.New(dec.Config{Window: testWindow, MaxOutput: maxOut})
		_, werr := d.Write(bad)
		cerr := d.Close()
		out := d.Output()
		if len(out) > int(maxOut) {
			t.Fatalf("byte %d: 越过输出上限", i)
		}
		if werr == nil && cerr == nil && !bytes.Equal(out, data) {
			t.Fatalf("byte %d: 静默返回错误内容", i)
		}
	}
}

func TestOutputLimit(t *testing.T) {
	bomb := craft(wire.Header(), lit('x'), wire.AppendBackref(nil, 1, 1<<40))
	d, _ := dec.New(dec.Config{Window: testWindow, MaxOutput: 100})
	_, err1 := d.Write(bomb)
	if !errors.Is(err1, dec.ErrOutputLimit) {
		t.Fatalf("炸弹未被提前拒绝: %v", err1)
	}
	if !bytes.Equal(d.Output(), []byte("x")) {
		t.Fatal("已输出内容未保留")
	}
	if _, err2 := d.Write(bomb); err2 != err1 {
		t.Fatal("终态后写入未返回同一错误")
	}
}
