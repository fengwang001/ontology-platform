package iter

import (
	"fmt"
	"ontology/segment"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func mkseg(t *testing.T, kvs ...[3]string) *segment.Reader {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.seg")
	w, err := segment.NewWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range kvs {
		if e[2] == "del" {
			err = w.Delete([]byte(e[0]))
		} else {
			err = w.Add([]byte(e[0]), []byte(e[1]))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := segment.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func drain(m *Merger) []string {
	var out []string
	for e, ok := m.Next(); ok; e, ok = m.Next() {
		s := fmt.Sprintf("%s=%s", e.Key, e.Val)
		if e.Deleted {
			s = fmt.Sprintf("%s=<del>", e.Key)
		}
		out = append(out, s)
	}
	return out
}

func TestMerge(t *testing.T) {
	cases := []struct {
		name     string
		srcs     [][]string // 每条 "key:val:level:seq"，val 为 del 表示删除标记
		emitTomb bool
		want     []string
	}{
		{"single source", [][]string{{"a:1:0:0", "b:2:0:0"}}, false, []string{"a=1", "b=2"}},
		{"lower level wins", [][]string{{"k:new:0:0"}, {"k:old:1:0"}}, false, []string{"k=new"}},
		{"higher seq wins in L0", [][]string{{"k:old:0:1"}, {"k:new:0:2"}}, false, []string{"k=new"}},
		{"tombstone hidden", [][]string{{"k:v:0:0", "x:v:0:0", "y:v:0:0"}, {"x:del:0:1"}}, false, []string{"k=v", "y=v"}},
		{"tombstone emitted", [][]string{{"x:old:1:0"}, {"x:del:0:0"}}, true, []string{"x=<del>"}},
		{"interleaved keys", [][]string{{"a:1:0:0", "c:3:0:0"}, {"b:2:1:0", "d:4:1:0"}}, false, []string{"a=1", "b=2", "c=3", "d=4"}},
		{"empty source", [][]string{{}, {"a:1:0:0"}}, false, []string{"a=1"}},
		{"all sources empty", [][]string{{}, {}}, false, nil},
		{"all same key 3 versions", [][]string{{"k:v0:2:0"}, {"k:v1:1:0"}, {"k:del:0:0"}}, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var srcs []*Source
			for _, spec := range c.srcs {
				var kvs [][3]string
				var lvl, seq int
				for _, s := range spec {
					parts := strings.Split(s, ":")
					k, v := parts[0], parts[1]
					lvl, _ = strconv.Atoi(parts[2])
					seq, _ = strconv.Atoi(parts[3])
					if v == "del" {
						kvs = append(kvs, [3]string{k, "", "del"})
					} else {
						kvs = append(kvs, [3]string{k, v, ""})
					}
				}
				srcs = append(srcs, NewSource(mkseg(t, kvs...), lvl, seq))
			}
			got := drain(NewMerger(srcs, c.emitTomb))
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestMergeBounds(t *testing.T) {
	const S, per = 5, 400
	var srcs []*Source
	for s := 0; s < S; s++ {
		var kvs [][3]string
		for i := 0; i < per; i++ { // 各段键互不相交、交错分布
			kvs = append(kvs, [3]string{fmt.Sprintf("k%06d", i*S+s), "v", ""})
		}
		srcs = append(srcs, NewSource(mkseg(t, kvs...), s, 0))
	}
	m := NewMerger(srcs, false)
	emitted := 0
	for _, ok := m.Next(); ok; _, ok = m.Next() {
		emitted++
	}
	if emitted != S*per {
		t.Fatalf("emitted=%d want %d", emitted, S*per)
	}
	if got := m.MaxResident(); got > S {
		t.Fatalf("MaxResident=%d > S=%d", got, S)
	}
	bound := int64(4 * emitted * 3) // 4*M*ceil(log2(S+1))=4*M*3
	if got := m.Compares(); got > bound {
		t.Fatalf("compares=%d > bound %d", got, bound)
	}
}
