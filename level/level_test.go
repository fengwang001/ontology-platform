package level

import (
	"errors"
	"fmt"
	"ontology/segment"
	"path/filepath"
	"testing"
)

func ingest(t *testing.T, s *Store, lvl int, kvs ...[3]string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "w.seg")
	w, err := segment.NewWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range kvs {
		var err error
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
	if _, err := s.Ingest(p, lvl); err != nil {
		t.Fatal(err)
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestGetStates(t *testing.T) {
	s := newStore(t)
	ingest(t, s, 2, [3]string{"old2", "L2", ""})    // 仅 L2 有
	ingest(t, s, 1, [3]string{"dup", "L1old", ""})  // L1 旧值
	ingest(t, s, 0, [3]string{"dup", "L0new", ""})  // L0 新值压 L1
	ingest(t, s, 0, [3]string{"gone", "", "del"})   // L0 删除标记
	ingest(t, s, 1, [3]string{"gone", "L1old", ""}) // 其下 L1 旧值
	ingest(t, s, 0, [3]string{"", "emptykey", ""})  // 空串键
	ingest(t, s, 0, [3]string{"emptyval", "", ""})  // 空字节串值
	cases := []struct {
		key     string
		want    segment.State
		wantVal string
	}{
		{"dup", segment.StateValue, "L0new"}, // 层小者胜
		{"gone", segment.StateDeleted, ""},   // 删除标记不复活旧值
		{"old2", segment.StateValue, "L2"},   // 仅深层有
		{"never", segment.StateAbsent, ""},   // 从未写过
		{"", segment.StateValue, "emptykey"}, // 空串键合法
		{"emptyval", segment.StateValue, ""}, // 空值≠删除
	}
	for _, c := range cases {
		val, st, err := s.Get([]byte(c.key))
		if err != nil {
			t.Fatal(err)
		}
		if st != c.want || string(val) != c.wantVal {
			t.Errorf("Get(%q)=(%q,%v), want (%q,%v)", c.key, val, st, c.wantVal, c.want)
		}
	}
}

func TestGetL0SeqOrder(t *testing.T) {
	s := newStore(t)
	ingest(t, s, 0, [3]string{"k", "v1", ""})
	ingest(t, s, 0, [3]string{"k", "v2", ""}) // 同层序号大者胜
	val, st, _ := s.Get([]byte("k"))
	if st != segment.StateValue || string(val) != "v2" {
		t.Fatalf("got (%q,%v), want (v2,StateValue)", val, st)
	}
}

func TestScan(t *testing.T) {
	s := newStore(t)
	ingest(t, s, 1, [3]string{"a", "1", ""}, [3]string{"c", "3", ""}, [3]string{"e", "5", ""})
	ingest(t, s, 0, [3]string{"b", "2", ""}, [3]string{"c", "3new", ""}, [3]string{"d", "", "del"})
	cases := []struct {
		name       string
		start, end string
		want       string // "k=v," 序列；空表示无结果；err 表示可判定错误
	}{
		{"left closed", "a", "c", "a=1,b=2,"},
		{"right open", "a", "e", "a=1,b=2,c=3new,"},
		{"newer wins", "c", "d", "c=3new,"},
		{"tombstone hidden", "d", "e", ""},
		{"full range", "a", "z", "a=1,b=2,c=3new,e=5,"},
		{"disjoint", "x", "z", ""},
		{"empty range", "b", "b", ""},
		{"reversed", "e", "a", "err"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.Scan([]byte(c.start), []byte(c.end))
			if c.want == "err" {
				if !errors.Is(err, ErrInvalidRange) {
					t.Fatalf("want ErrInvalidRange, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			out := ""
			for _, e := range got {
				out += fmt.Sprintf("%s=%s,", e.Key, e.Val)
			}
			if out != c.want {
				t.Fatalf("got %q, want %q", out, c.want)
			}
		})
	}
}
