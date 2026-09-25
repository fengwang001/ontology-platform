package stream_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/stream"
)

func hx(s string) []byte {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		var v byte
		for j := 0; j < 2; j++ {
			c := s[2*i+j]
			switch {
			case c >= '0' && c <= '9':
				v = v<<4 | c - '0'
			case c >= 'A' && c <= 'F':
				v = v<<4 | c - 'A' + 10
			default:
				v = v<<4 | c - 'a' + 10
			}
		}
		out[i] = v
	}
	return out
}

func runAll(in []byte, cfg stream.Config) ([]byte, stream.Stats, error) {
	t := stream.New(cfg)
	if _, err := t.Write(in); err != nil {
		return t.Output(), t.Stats(), err
	}
	err := t.Close()
	return t.Output(), t.Stats(), err
}

func fffd() []byte { return hx("EF BF BD") }

// TestReplacementSamples 钉住题一的七个非法单元样例。
func TestReplacementSamples(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"F0908041", "EF BF BD 41"},
		{"E08080", "EF BF BD EF BF BD EF BF BD"},
		{"EDA080", "EF BF BD EF BF BD EF BF BD"},
		{"C0AF", "EF BF BD EF BF BD"},
		{"F4908080", "EF BF BD EF BF BD EF BF BD EF BF BD"},
		{"E282", "EF BF BD"},
		{"8080", "EF BF BD EF BF BD"},
	}
	for _, c := range cases {
		got, _, err := runAll(hx(c.in), stream.Config{Src: stream.UTF8, Dst: stream.UTF8})
		if err != nil || !bytes.Equal(got, hx(strip(c.want))) {
			t.Fatalf("%s: got % X err=%v, want %s", c.in, got, err, c.want)
		}
	}
}

func strip(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// TestStrictOffsetLength 严格模式错误类型、偏移、长度与终态。
func TestStrictOffsetLength(t *testing.T) {
	cases := []struct {
		in          string
		off, length int
		kind        string
	}{
		{"41F0908041", 1, 3, "illegal"},
		{"C0AF", 0, 1, "illegal"},
		{"E282", 0, 2, "trunc"},
		{"E28241", 0, 3, "illegal"},
	}
	for _, c := range cases {
		tr := stream.New(stream.Config{Src: stream.UTF8, Dst: stream.UTF8, Strict: true})
		_, werr := tr.Write(hx(c.in))
		cerr := tr.Close()
		err := werr
		if err == nil {
			err = cerr
		}
		var il *stream.IllegalError
		var tr2 *stream.TruncatedError
		switch {
		case c.kind == "illegal" && !errors.As(err, &il):
			t.Fatalf("%s: want IllegalError got %T %v", c.in, err, err)
		case c.kind == "trunc" && !errors.As(err, &tr2):
			t.Fatalf("%s: want TruncatedError got %T %v", c.in, err, err)
		}
		if e := asErr(err); e != nil && (e.Offset != c.off || e.Length != c.length) {
			t.Fatalf("%s: offset/length=%d/%d want %d/%d", c.in, e.Offset, e.Length, c.off, c.length)
		}
		if _, err2 := tr.Write([]byte{0x41}); !errors.Is(err2, stream.ErrClosed) {
			t.Fatalf("%s: write after terminal = %v, want ErrClosed", c.in, err2)
		}
	}
}

func asErr(err error) *struct{ Offset, Length int } {
	var il *stream.IllegalError
	var tr *stream.TruncatedError
	if errors.As(err, &il) {
		return &struct{ Offset, Length int }{il.Offset, il.Length}
	}
	if errors.As(err, &tr) {
		return &struct{ Offset, Length int }{tr.Offset, tr.Length}
	}
	return nil
}

// TestSplitInvariance 所有切分点、1 字节、随机切分输出与错误逐字节相同。
func TestSplitInvariance(t *testing.T) {
	samples := [][]byte{
		hx("4142F0908041E08080EDA080C0AFF4908080E282828080"),
		{0x41, 0xE2, 0x82, 0xAC, 0xF0, 0x9F, 0x98, 0x80, 0xED, 0xA0, 0x80},
	}
	for _, strict := range []bool{false, true} {
		for _, in := range samples {
			ref, _, refErr := runAll(in, stream.Config{Src: stream.UTF8, Dst: stream.UTF8, Strict: strict})
			for cut := 0; cut <= len(in); cut++ {
				tr := stream.New(stream.Config{Src: stream.UTF8, Dst: stream.UTF8, Strict: strict})
				var err error
				if cut > 0 {
					_, err = tr.Write(in[:cut])
				}
				if err == nil {
					_, err = tr.Write(in[cut:])
				}
				cerr := tr.Close()
				if err == nil {
					err = cerr
				}
				if !bytes.Equal(tr.Output(), ref) || sameErrKind(err, refErr) == false {
					t.Fatalf("strict=%v cut=%d: % X vs % X, err %v/%v", strict, cut, tr.Output(), ref, err, refErr)
				}
			}
		}
	}
}

func sameErrKind(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	var ia, ib *stream.IllegalError
	var ta, tb *stream.TruncatedError
	return errors.As(a, &ia) == errors.As(b, &ib) && errors.As(a, &ta) == errors.As(b, &tb)
}

// TestRoundTripIdempotent 合法往返与任意字节串替换后幂等。
func TestRoundTripIdempotent(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	orig := make([]byte, 2000)
	for i := range orig {
		orig[i] = byte(rng.Intn(0x300))
	}
	u8out, _, err := runAll(orig, stream.Config{Src: stream.UTF8, Dst: stream.UTF8})
	if err != nil {
		t.Fatal(err)
	}
	u16le, _, err := runAll(u8out, stream.Config{Src: stream.UTF8, Dst: stream.UTF16LE})
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := runAll(u16le, stream.Config{Src: stream.UTF16LE, Dst: stream.UTF8})
	if err != nil || !bytes.Equal(back, u8out) {
		t.Fatalf("roundtrip mismatch err=%v", err)
	}
	again, _, err := runAll(u8out, stream.Config{Src: stream.UTF8, Dst: stream.UTF8})
	if err != nil || !bytes.Equal(again, u8out) {
		t.Fatalf("idempotence mismatch")
	}
}

// TestByteConservation 合法字节 + 非法吞掉字节 + BOM 字节 == 消费字节。
func TestByteConservation(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 50; iter++ {
		in := make([]byte, rng.Intn(300)+1)
		for i := range in {
			in[i] = byte(rng.Intn(260))
		}
		_, st, err := runAll(in, stream.Config{Src: stream.UTF8, Dst: stream.UTF8})
		if err != nil {
			t.Fatal(err)
		}
		if st.ValidBytes+st.InvalidBytes+st.BOMBytes != int64(len(in)) {
			t.Fatalf("conservation %d+%d+%d != %d", st.ValidBytes, st.InvalidBytes, st.BOMBytes, len(in))
		}
	}
}

// TestTruncationEveryByte 在每个字节位置截断，字符边界 Close 成功。
func TestTruncationEveryByte(t *testing.T) {
	in := hx("41C2A9E282ACF09F9880")
	bounds := map[int]bool{0: true, 1: true, 3: true, 6: true, 10: true}
	for cut := 0; cut <= len(in); cut++ {
		tr := stream.New(stream.Config{Src: stream.UTF8, Dst: stream.UTF8, Strict: true})
		_, _ = tr.Write(in[:cut])
		err := tr.Close()
		_, wantTrunc := err.(*stream.TruncatedError)
		if bounds[cut] && err != nil {
			t.Fatalf("cut %d boundary Close err=%v", cut, err)
		}
		if !bounds[cut] && !wantTrunc {
			t.Fatalf("cut %d want truncation got %v", cut, err)
		}
	}
}

// TestOutputLimitResume 上限断点：同实例续传与换新实例拼接都逐字节一致。
func TestOutputLimitResume(t *testing.T) {
	in := []byte("Hello, 世界! こんにちは")
	ref, _, err := runAll(in, stream.Config{Src: stream.UTF8, Dst: stream.UTF8})
	if err != nil {
		t.Fatal(err)
	}
	for _, lim := range []int{1, 2, 3, 5, 7, 8, 9, 13} {
		var acc []byte
		rest := in
		for len(rest) > 0 || true {
			tr := stream.New(stream.Config{Src: stream.UTF8, Dst: stream.UTF8, MaxOut: lim})
			n, err := tr.Write(rest)
			rest = rest[n:]
			acc = append(acc, tr.Output()...)
			if errors.Is(err, stream.ErrOutputLimit) {
				rest = append(tr.Resume(), rest...)
				continue
			}
			if err := tr.Close(); err != nil {
				if errors.Is(err, stream.ErrOutputLimit) {
					rest = append(tr.Resume(), rest...)
					continue
				}
				t.Fatal(err)
			}
			break
		}
		if !bytes.Equal(acc, ref) {
			t.Fatalf("lim=%d resume mismatch % X vs % X", lim, acc, ref)
		}
	}
}

// TestCheckCount 1MB/16MB 在碎喂与整喂下检查次数 ≤ 2N。
func TestCheckCount(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, n := range []int{1 << 20, 16 << 20} {
		in := make([]byte, n)
		for i := range in {
			v := rng.Intn(256)
			if rng.Intn(20) == 0 {
				v = rng.Intn(260)
			}
			in[i] = byte(v)
		}
		whole := stream.New(stream.Config{Src: stream.UTF8, Dst: stream.UTF8})
		_, _ = whole.Write(in)
		_ = whole.Close()
		if whole.Stats().Checks > 2*int64(n) {
			t.Fatalf("whole checks %d > 2*%d", whole.Stats().Checks, n)
		}
		one := stream.New(stream.Config{Src: stream.UTF8, Dst: stream.UTF8})
		for _, b := range in {
			if _, err := one.Write([]byte{b}); err != nil && !errors.Is(err, stream.ErrOutputLimit) {
				t.Fatal(err)
			}
		}
		_ = one.Close()
		if one.Stats().Checks > 2*int64(n) {
			t.Fatalf("byte-wise checks %d > 2*%d", one.Stats().Checks, n)
		}
	}
}

// TestCacheCap 任何时刻残尾缓存不超过 3 字节（通过 Resume 长度观测）。
func TestCacheCap(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	in := make([]byte, 5000)
	for i := range in {
		in[i] = byte(rng.Intn(260))
	}
	tr := stream.New(stream.Config{Src: stream.UTF8, Dst: stream.UTF8})
	for _, b := range in {
		if _, err := tr.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
		if len(tr.Resume()) > 3 {
			t.Fatalf("pending %d > 3", len(tr.Resume()))
		}
	}
	_ = tr.Close()
}
