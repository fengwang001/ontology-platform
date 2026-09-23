package verify

import (
	"encoding/binary"
	"errors"
	"fmt"
	"ontology/level"
	"ontology/segment"
	"os"
	"path/filepath"
	"testing"
)

// 对 500 条目段逐字节截断，每个截断点都要正确分类；抽样断言可恢复前缀。
func TestTruncationClassification(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.seg")
	w, err := segment.NewWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ { // 每条 30B：8 头 + 9 键 + 9 值 + 4 CRC
		if err := w.Add([]byte(fmt.Sprintf("key%06d", i)), []byte(fmt.Sprintf("val%06d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	size := len(full)
	indexOff := int(binary.LittleEndian.Uint64(full[12:20]))
	indexEnd := indexOff + int(binary.LittleEndian.Uint64(full[20:28]))
	counts := map[error]int{}
	for n := size - 1; n >= 1; n-- {
		if err := os.Truncate(p, int64(n)); err != nil {
			t.Fatal(err)
		}
		var want error
		switch {
		case n < 36:
			want = segment.ErrHeaderIncomplete
		case n < indexOff:
			want = segment.ErrEntryTruncated
		case n < indexEnd:
			want = segment.ErrIndexIncomplete
		default:
			want = segment.ErrCRCMismatch
		}
		if got := CheckSegment(p); !errors.Is(got, want) {
			t.Fatalf("n=%d: got %v, want %v", n, got, want)
		}
		counts[want]++
		if n%97 == 0 || n == size-1 || n == indexOff-1 || n == indexEnd-1 || n == 35 {
			got, _ := segment.RecoverPrefix(p)
			wantN := 500
			if n < 36 {
				wantN = 0
			} else if n < indexOff {
				wantN = (n - 36) / 30
			}
			if len(got) != wantN {
				t.Fatalf("n=%d: prefix=%d, want %d", n, len(got), wantN)
			}
		}
	}
	for _, e := range []error{segment.ErrHeaderIncomplete, segment.ErrEntryTruncated, segment.ErrIndexIncomplete, segment.ErrCRCMismatch} {
		if counts[e] == 0 {
			t.Fatalf("category %v never observed", e)
		}
	}
	t.Logf("size=%d index=[%d,%d) counts=%v", size, indexOff, indexEnd, counts)
}

func TestCheckLevels(t *testing.T) {
	meta := func(lvl, seq int, min, max string) level.Meta {
		return level.Meta{Path: fmt.Sprintf("L%d-%06d.seg", lvl, seq), Level: lvl, Seq: seq, MinKey: min, MaxKey: max}
	}
	cases := []struct {
		name   string
		snap   map[int][]level.Meta
		want   int // 违例数；-1 表示断言重叠区间
		lo, hi string
	}{
		{"L0 overlap allowed", map[int][]level.Meta{0: {meta(0, 1, "a", "m"), meta(0, 2, "k", "z")}}, 0, "", ""},
		{"L1 disjoint", map[int][]level.Meta{1: {meta(1, 1, "a", "j"), meta(1, 2, "k", "z")}}, 0, "", ""},
		{"L1 touching overlaps", map[int][]level.Meta{1: {meta(1, 1, "a", "j"), meta(1, 2, "j", "z")}}, -1, "j", "j"},
		{"L1 overlap", map[int][]level.Meta{1: {meta(1, 1, "a", "m"), meta(1, 2, "k", "z")}}, -1, "k", "m"},
		{"L2 nested overlap", map[int][]level.Meta{2: {meta(2, 1, "a", "z"), meta(2, 2, "k", "m")}}, -1, "k", "m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CheckLevels(c.snap)
			if c.want >= 0 {
				if len(got) != c.want {
					t.Fatalf("violations=%v, want %d", got, c.want)
				}
				return
			}
			if len(got) != 1 || got[0].Lo != c.lo || got[0].Hi != c.hi {
				t.Fatalf("got %+v, want overlap [%s,%s]", got, c.lo, c.hi)
			}
			if got[0].A == "" || got[0].B == "" || got[0].A == got[0].B {
				t.Fatalf("overlap must name both segments: %+v", got[0])
			}
		})
	}
}
