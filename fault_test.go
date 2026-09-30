package ontology_test

import "bytes"
import "errors"
import "testing"
import "ontology/dec"
import "ontology/enc"
import "ontology/match"
import "ontology/window"
import "ontology/wire"

type corruptCase struct {
	name string
	s    []byte
	want error
	off  int64
}

func corruptStreams(valid []byte) []corruptCase {
	badMagic := bytes.Clone(valid)
	badMagic[0] = 'X'
	badVersion := bytes.Clone(valid)
	badVersion[3] = 99
	zeroDist := wire.AppendBackref(wire.Header(), 0, 5)
	tooFar := wire.AppendLiteral(wire.Header(), []byte("ab"))
	tooFar = wire.AppendBackref(tooFar, 3, 1)
	tooLarge := wire.AppendLiteral(wire.Header(), []byte("ab"))
	tooLarge = wire.AppendBackref(tooLarge, 1<<20+1, 1)
	overflow := append(wire.Header(), wire.TagLiteral)
	overflow = append(overflow, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02)
	lenBad := wire.AppendLiteral(wire.Header(), []byte("ab"))
	lenBad = wire.AppendEnd(lenBad, 3, wire.Checksum([]byte("ab")))
	sumBad := wire.AppendLiteral(wire.Header(), []byte("ab"))
	sumBad = wire.AppendEnd(sumBad, 2, wire.Checksum([]byte("ab"))^1)
	trailing := append(bytes.Clone(valid), 0)
	return []corruptCase{
		{"badMagic", badMagic, wire.ErrBadMagic, 0},
		{"badVersion", badVersion, wire.ErrBadVersion, 3},
		{"zeroDist", zeroDist, wire.ErrZeroDistance, 6},
		{"distTooFar", tooFar, wire.ErrDistanceTooFar, -1},
		{"distTooLarge", tooLarge, wire.ErrDistanceTooLarge, -1},
		{"varintOverflow", overflow, wire.ErrVarintOverflow, -1},
		{"lengthMismatch", lenBad, wire.ErrLengthMismatch, -1},
		{"checksumMismatch", sumBad, wire.ErrChecksumMismatch, -1},
		{"trailingData", trailing, wire.ErrTrailingData, -1},
	}
}

func TestCorruption(t *testing.T) {
	valid := encode(t, []byte("hello hello hello"), 64)
	for _, c := range corruptStreams(valid) {
		d, _ := dec.New(dcfg())
		_, err := d.Write(c.s)
		if err == nil {
			err = d.Close()
		}
		if !errors.Is(err, c.want) {
			t.Errorf("%s: 得到 %v，期望 %v", c.name, err, c.want)
			continue
		}
		var we *wire.Error
		if !errors.As(err, &we) {
			t.Errorf("%s: 错误未携带偏移", c.name)
		} else if c.off >= 0 && we.Offset != c.off {
			t.Errorf("%s: 偏移 %d，期望 %d", c.name, we.Offset, c.off)
		}
	}
}

func TestOutputBomb(t *testing.T) {
	cfg := dec.Config{Window: 1 << 20, MaxOutput: 1 << 30}
	// 回指声明长度 2^40：写出前拒绝，已输出保留，进入终态
	stream := wire.AppendLiteral(wire.Header(), []byte("a"))
	stream = wire.AppendBackref(stream, 1, 1<<40)
	d, _ := dec.New(cfg)
	_, err := d.Write(stream)
	if !errors.Is(err, wire.ErrOutputLimit) {
		t.Fatalf("炸弹未被拒绝: %v", err)
	}
	if len(d.Output()) != 1 {
		t.Error("拒绝前已输出的内容应保留")
	}
	if _, err2 := d.Write([]byte{0}); !errors.Is(err2, wire.ErrOutputLimit) {
		t.Error("终态后写入应返回同一错误")
	}
	// 字面量声明长度超限同样拒绝
	s2 := append(wire.Header(), wire.TagLiteral)
	s2 = wire.AppendUvarint(s2, 1<<40)
	d2, _ := dec.New(cfg)
	if _, err := d2.Write(s2); !errors.Is(err, wire.ErrOutputLimit) {
		t.Error("字面量超限未被拒绝")
	}
}

func TestTruncationTraversal(t *testing.T) {
	data := samples()["mixed"][:3000]
	stream := encode(t, data, 512)
	for cut := 0; cut < len(stream); cut++ {
		d, _ := dec.New(dcfg())
		d.Write(stream[:cut])
		if err := d.Close(); !errors.Is(err, wire.ErrTruncated) {
			t.Fatalf("cut=%d: 期望截断错误，得到 %v", cut, err)
		}
		if !bytes.HasPrefix(data, d.Output()) {
			t.Fatalf("cut=%d: 输出不是原文前缀", cut)
		}
	}
	out, err := decode(t, stream, 1<<20)
	if err != nil || !bytes.Equal(out, data) {
		t.Fatal("完整流应成功")
	}
}

func TestFlipTraversal(t *testing.T) {
	data := samples()["mixed"][:2000]
	stream := encode(t, data, 256)
	for i := range stream {
		flipped := bytes.Clone(stream)
		flipped[i] ^= 1 << (i % 8)
		d, _ := dec.New(dcfg())
		_, werr := d.Write(flipped)
		cerr := d.Close()
		if werr == nil && cerr == nil && !bytes.Equal(d.Output(), data) {
			t.Fatalf("字节 %d 翻转后静默产出错误内容", i)
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	var buf bytes.Buffer
	if _, err := enc.New(&buf, enc.Config{Window: 0, MaxChain: 1}); !errors.Is(err, window.ErrZeroCapacity) {
		t.Error("窗口为 0 应拒绝")
	}
	if _, err := enc.New(&buf, enc.Config{Window: 1, MaxChain: 0}); !errors.Is(err, match.ErrZeroChain) {
		t.Error("链长为 0 应拒绝")
	}
	if _, err := dec.New(dec.Config{Window: 0, MaxOutput: 1}); !errors.Is(err, window.ErrZeroCapacity) {
		t.Error("解压窗口为 0 应拒绝")
	}
	if _, err := enc.CompressParallel([]byte("x"), 0, 1, cfg); !errors.Is(err, enc.ErrInvalidBlock) {
		t.Error("块大小为 0 应拒绝")
	}
	if _, err := enc.CompressParallel([]byte("x"), 1, 0, cfg); !errors.Is(err, enc.ErrInvalidBlock) {
		t.Error("workers 为 0 应拒绝")
	}
	if _, err := enc.CompressParallel([]byte("x"), 1, 1, enc.Config{}); !errors.Is(err, window.ErrZeroCapacity) {
		t.Error("并行压缩窗口为 0 应拒绝")
	}
}
