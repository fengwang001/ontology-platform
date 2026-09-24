package stream_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/stream"
)

// run 按固定块大小（0=整段）喂入并 Close（整段即完整流）。
func run(cfg stream.Config, in []byte, chunk int) ([]byte, stream.Stats, error) {
	tr := stream.New(cfg)
	for off := 0; off < len(in); {
		n := chunk
		if n <= 0 || n > len(in)-off {
			n = len(in) - off
		}
		c, err := tr.Write(in[off : off+n])
		off += c
		if err != nil {
			return tr.Output(), tr.Stats(), err
		}
	}
	err := tr.Close()
	return tr.Output(), tr.Stats(), err
}

// runNoClose 只喂入不 Close，返回 Write 阶段的最终错误（中段语义）。
func runNoClose(cfg stream.Config, in []byte) ([]byte, stream.Stats, error) {
	tr := stream.New(cfg)
	off := 0
	var err error
	for off < len(in) {
		var c int
		c, err = tr.Write(in[off:])
		off += c
		if err != nil {
			break
		}
	}
	return tr.Output(), tr.Stats(), err
}

// cutCases 返回切分方案：不切、每个字节边界各切一刀、全 1 字节切分。
func cutCases(n int) [][]int {
	res := [][]int{nil}
	for i := 1; i < n; i++ {
		res = append(res, []int{i})
	}
	all := make([]int, 0, n)
	for i := 1; i < n; i++ {
		all = append(all, i)
	}
	return append(res, all)
}

// runCuts 按 cuts 切分喂入；closeAt>0 表示最后一段后调用 Close（完整流），
// 否则只 Write，段尾残留半截字符由后续段接续。
func runCuts(cfg stream.Config, in []byte, cuts []int, doClose bool) ([]byte, stream.Stats, error) {
	tr := stream.New(cfg)
	prev := 0
	for _, ct := range cuts {
		if _, err := tr.Write(in[prev:ct]); err != nil {
			return tr.Output(), tr.Stats(), err
		}
		prev = ct
	}
	_, err := tr.Write(in[prev:])
	if err == nil && doClose {
		err = tr.Close()
	}
	return tr.Output(), tr.Stats(), err
}

func sameClass(a, b error) bool {
	for _, s := range []error{stream.ErrIllegal, stream.ErrTruncated, stream.ErrOutputLimit} {
		if errors.Is(a, s) != errors.Is(b, s) {
			return false
		}
	}
	return true
}

var sampleCases = []struct {
	in      []byte
	badWant int64
}{
	{[]byte{0xF0, 0x90, 0x80, 0x41}, 1},
	{[]byte{0xE0, 0x80, 0x80}, 3},
	{[]byte{0xED, 0xA0, 0x80}, 3},
	{[]byte{0xC0, 0xAF}, 2},
	{[]byte{0xF4, 0x90, 0x80, 0x80}, 4},
	{[]byte{0xE2, 0x82}, 1},
	{[]byte{0x80, 0x80}, 2},
}

func TestReplacementSamples(t *testing.T) {
	for i, c := range sampleCases {
		out, st, err := run(stream.Config{Dir: stream.U8toU16LE}, c.in, 0)
		if err != nil || st.BadUnits != c.badWant {
			t.Fatalf("sample %d: bad=%d err=%v", i, st.BadUnits, err)
		}
		if bytes.Count(out, []byte{0xFD, 0xFF}) != int(c.badWant) {
			t.Fatalf("sample %d: FFFD count in %x", i, out)
		}
	}
}

func TestSplitInvariance(t *testing.T) {
	ins := [][]byte{
		{0xF0, 0x90, 0x80, 0x41},
		{0xE0, 0x80, 0x80},
		{0xC3, 0x80, 0x80},
		{'A', 0xE2, 0x82, 0xAC, 'B'},
		{0xEF, 0xBB, 0xBF, 'x', 0xEF, 0xBB, 0xBF},
		{0xE2, 0x82},
	}
	for _, dir := range []stream.Dir{stream.U8toU16LE, stream.U8toU16BE} {
		for _, in := range ins {
			base, bst, berr := run(stream.Config{Dir: dir}, in, 0)
			for _, cuts := range cutCases(len(in)) {
				out, st, err := runCuts(stream.Config{Dir: dir}, in, cuts, true)
				if !bytes.Equal(out, base) || !sameClass(err, berr) {
					t.Fatalf("dir=%d cuts=%v mismatch", dir, cuts)
				}
				if st.BadUnits != bst.BadUnits || st.Consumed != bst.Consumed {
					t.Fatalf("dir=%d cuts=%v stats", dir, cuts)
				}
			}
		}
	}
}
