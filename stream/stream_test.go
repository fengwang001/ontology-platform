package stream_test

import (
	"errors"
	"testing"

	"ontology/stream"
	"ontology/u8"
)

func runBytes(in []byte, cfg stream.Config, chunk int) ([]byte, stream.Stats, error) {
	tr := stream.New(cfg)
	if chunk <= 0 {
		chunk = len(in) + 1
	}
	for i := 0; i < len(in); i += chunk {
		j := i + chunk
		if j > len(in) {
			j = len(in)
		}
		if _, err := tr.Write(in[i:j]); err != nil {
			return tr.Output(), tr.Stats(), err
		}
	}
	err := tr.Close()
	return tr.Output(), tr.Stats(), err
}

func runSplits(in []byte, cfg stream.Config, at int) ([]byte, error) {
	tr := stream.New(cfg)
	if _, err := tr.Write(in[:at]); err != nil {
		return tr.Output(), err
	}
	if _, err := tr.Write(in[at:]); err != nil {
		return tr.Output(), err
	}
	err := tr.Close()
	return tr.Output(), err
}

var rep = stream.Config{From: stream.UTF8, To: stream.UTF8}

func TestSevenSamples(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"\xF0\x90\x80\x41", "\uFFFD\x41"},
		{"\xE0\x80\x80", "\uFFFD\uFFFD\uFFFD"},
		{"\xED\xA0\x80", "\uFFFD\uFFFD\uFFFD"},
		{"\xC0\xAF", "\uFFFD\uFFFD"},
		{"\xF4\x90\x80\x80", "\uFFFD\uFFFD\uFFFD\uFFFD"},
		{"\xE2\x82", "\uFFFD"},
		{"\x80\x80", "\uFFFD\uFFFD"},
	}
	for _, c := range cases {
		got, _, err := runBytes([]byte(c.in), rep, 1)
		if err != nil || string(got) != c.want {
			t.Errorf("% x -> %q err=%v, want %q", c.in, got, err, c.want)
		}
	}
}

func TestStrictOffsetLen(t *testing.T) {
	cases := []struct {
		in  string
		off int64
		n   int
	}{
		{"A\xF0\x90\x80\x41", 1, 3},
		{"\xE0\x80\x80", 0, 1},
		{"\xED\xA0\x80", 0, 1},
		{"ab\xC0\xAF", 2, 1},
		{"\x80\x80", 1, 1},
	}
	strict := stream.Config{From: stream.UTF8, To: stream.UTF8, Strict: true}
	for _, c := range cases {
		_, _, err := runBytes([]byte(c.in), strict, 1)
		var ue *stream.UnitError
		if !errors.As(err, &ue) || !errors.Is(err, stream.ErrIllegal) ||
			ue.Offset != c.off || ue.Length != c.n {
			t.Errorf("% x err=%v gotOff=%d gotLen=%d want %d/%d",
				c.in, err, ue.Offset, ue.Length, c.off, c.n)
		}
	}
}

func TestSplitInvariance(t *testing.T) {
	inputs := []string{
		"abc", "é漢字🙂", "\xF0\x90\x80\x41", "\xE0\x80\x80", "\xED\xA0\x80",
		"a\xC3\xA9b🙂\x80z\xE2\x82\xAC", "\xEF\xBB\xBFabc\xEF\xBB\xBF",
	}
	for _, in := range inputs {
		base, _, berr := runBytes([]byte(in), rep, len(in)+1)
		for at := 0; at <= len(in); at++ {
			for _, chunk := range []int{1, 2, 3, 7} {
				got, _, err := runBytes([]byte(in), rep, chunk)
				if err != berr || string(got) != string(base) {
					t.Fatalf("in=% x chunk=%d mismatch", in, chunk)
				}
			}
			g2, e2 := runSplits([]byte(in), rep, at)
			if e2 != berr || string(g2) != string(base) {
				t.Fatalf("in=% x at=%d mismatch", in, at)
			}
		}
	}
}

func TestTruncationEveryByte(t *testing.T) {
	in := []byte("aé🙂")
	strict := stream.Config{From: stream.UTF8, To: stream.UTF8, Strict: true}
	for at := 0; at <= len(in); at++ {
		tr := stream.New(strict)
		tr.Write(in[:at])
		err := tr.Close()
		boundary := true
		if at > 0 {
			var d u8.Decoder
			for _, x := range in[:at] {
				d.Step(x)
			}
			boundary = !d.InProgress()
		}
		if boundary && err != nil {
			t.Errorf("at=%d unexpected %v", at, err)
		}
		if !boundary && !errors.Is(err, stream.ErrTruncated) {
			t.Errorf("at=%d want truncated got %v", at, err)
		}
	}
}

func TestTruncatedVsIllegal(t *testing.T) {
	strict := stream.Config{From: stream.UTF8, To: stream.UTF8, Strict: true}
	if _, _, e1 := runBytes([]byte("\xE2\x82"), strict, 2); !errors.Is(e1, stream.ErrTruncated) {
		t.Fatal("E2 82 EOF want truncated")
	}
	if _, _, e2 := runBytes([]byte("\xE2\x41"), strict, 2); !errors.Is(e2, stream.ErrIllegal) {
		t.Fatal("E2 41 want illegal")
	}
}

func TestBOM(t *testing.T) {
	// 流首 BOM 被丢弃（默认不输出）；流中 BOM 保留。
	in := []byte("\xEF\xBB\xBFa\xEF\xBB\xBFb")
	out, st, err := runBytes(in, rep, 1)
	if err != nil || string(out) != "a\xEF\xBB\xBFb" || st.BOMBytes != 3 {
		t.Fatalf("out=%q bom=%d err=%v", out, st.BOMBytes, err)
	}
	emit := stream.Config{From: stream.UTF8, To: stream.UTF8, EmitBOM: true}
	out2, _, _ := runBytes(in, emit, 2)
	if string(out2) != "\xEF\xBB\xBFa\xEF\xBB\xBFb" {
		t.Fatalf("emit out=% x", out2)
	}
}

func TestConservation(t *testing.T) {
	in := []byte("aé🙂\xF0\x90\x80\x41\x80\xEF\xBB\xBFz\xE2")
	for _, chunk := range []int{1, 3, len(in) + 1} {
		out, st, _ := runBytes(in, rep, chunk)
		if got := st.AcceptedBytes + st.BadBytes + st.BOMBytes; got != int64(len(in)) {
			t.Fatalf("chunk=%d conservation %d != %d", chunk, got, len(in))
		}
		if !validUTF8(out) {
			t.Fatal("replacement output not valid utf-8")
		}
	}
}

func validUTF8(b []byte) bool {
	var d u8.Decoder
	for _, x := range b {
		for _, e := range d.Step(x) {
			if e.Kind == u8.Bad {
				return false
			}
		}
	}
	return !d.InProgress()
}

func TestTerminalReuse(t *testing.T) {
	strict := stream.Config{From: stream.UTF8, To: stream.UTF8, Strict: true}
	tr := stream.New(strict)
	_, e1 := tr.Write([]byte("\x80"))
	_, e2 := tr.Write([]byte("x"))
	if e1 != e2 || !errors.Is(e2, stream.ErrIllegal) {
		t.Fatalf("terminal reuse %v %v", e1, e2)
	}
}

func TestLimitResume(t *testing.T) {
	in := []byte("abcdefgh")
	full, _, _ := runBytes(in, rep, len(in)+1)
	const lim = 3
	var pieced []byte
	for off := 0; off < len(in); {
		tr := stream.New(stream.Config{From: stream.UTF8, To: stream.UTF8, Limit: lim, Resume: off > 0})
		n, err := tr.Write(in[off:])
		pieced = append(pieced, tr.Output()...)
		off += n
		if err == nil {
			tr.Close()
			break
		}
		if !errors.Is(err, stream.ErrLimit) || n == 0 && off < len(in) {
			t.Fatalf("limit err=%v n=%d", err, n)
		}
	}
	if string(pieced) != string(full) {
		t.Fatalf("pieced=%q full=%q", pieced, full)
	}
}
