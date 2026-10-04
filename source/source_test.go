package source_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"ontology/source"
)

func mustPut(t *testing.T, s *source.Source, id, body string) uint64 {
	t.Helper()
	seq, err := s.Put(id, []byte(body))
	if err != nil {
		t.Fatalf("Put(%q): %v", id, err)
	}
	return seq
}

func TestSeqAllocation(t *testing.T) {
	s := source.New()
	if seq := mustPut(t, s, "a", "1"); seq != 1 {
		t.Fatalf("seq = %d, want 1", seq)
	}
	if seq := mustPut(t, s, "b", "2"); seq != 2 {
		t.Fatalf("seq = %d, want 2", seq)
	}
	seq, err := s.Delete("a")
	if err != nil || seq != 3 {
		t.Fatalf("Delete(a) = (%d, %v), want (3, nil)", seq, err)
	}
	// 被拒绝的操作不占号。
	if _, err := s.Delete("a"); !errors.Is(err, source.ErrDocNotFound) {
		t.Fatalf("Delete(a) again: %v, want ErrDocNotFound", err)
	}
	if _, err := s.Delete("missing"); !errors.Is(err, source.ErrDocNotFound) {
		t.Fatalf("Delete(missing): %v, want ErrDocNotFound", err)
	}
	if _, err := s.Put("", nil); !errors.Is(err, source.ErrInvalidID) {
		t.Fatalf("Put(\"\"): %v, want ErrInvalidID", err)
	}
	if seq := mustPut(t, s, "c", "3"); seq != 4 {
		t.Fatalf("被拒操作占号了: seq = %d, want 4", seq)
	}
	doc, ok := s.Get("b")
	if !ok || doc.Seq != 2 || string(doc.Body) != "2" {
		t.Fatalf("Get(b) = %+v, %v", doc, ok)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("a 应已删除")
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		body    int // -1 表示 Delete
		wantErr error
	}{
		{"id 空", "", 1, source.ErrInvalidID},
		{"id 513 字节", strings.Repeat("i", 513), 1, source.ErrInvalidID},
		{"id 1 字节", "x", 1, nil},
		{"id 512 字节", strings.Repeat("i", 512), 1, nil},
		{"body 65537 字节", "big", 65537, source.ErrInvalidBody},
		{"body 65536 字节", "max", 65536, nil},
		{"body 0 字节", "empty", 0, nil},
		{"delete id 空", "", -1, source.ErrInvalidID},
		{"delete id 513 字节", strings.Repeat("i", 513), -1, source.ErrInvalidID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := source.New()
			var err error
			if tc.body < 0 {
				_, err = s.Delete(tc.id)
			} else {
				_, err = s.Put(tc.id, make([]byte, tc.body))
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestOverwrite(t *testing.T) {
	s := source.New()
	mustPut(t, s, "a", "1")
	seq := mustPut(t, s, "a", "2")
	doc, _ := s.Get("a")
	if doc.Seq != seq || string(doc.Body) != "2" {
		t.Fatalf("覆盖失败: %+v", doc)
	}
	if n := len(s.LiveDocs()); n != 1 {
		t.Fatalf("存活文档数 = %d, want 1", n)
	}
}

func TestSwitchedRejectionOrder(t *testing.T) {
	s := source.New()
	mustPut(t, s, "a", "1")
	s.Lock()
	s.EndForward(true)
	s.Unlock()
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"Put 参数非法优先于已切换", func() error { _, e := s.Put("", nil); return e }, source.ErrInvalidID},
		{"Put body 非法优先于已切换", func() error { _, e := s.Put("a", make([]byte, 65537)); return e }, source.ErrInvalidBody},
		{"Put 已切换", func() error { _, e := s.Put("a", []byte("x")); return e }, source.ErrSwitched},
		{"Delete 参数非法优先于已切换", func() error { _, e := s.Delete(""); return e }, source.ErrInvalidID},
		{"Delete 已切换优先于文档不存在", func() error { _, e := s.Delete("missing"); return e }, source.ErrSwitched},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.op(); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLiveDocsSorted(t *testing.T) {
	s := source.New()
	mustPut(t, s, "c", "1")
	mustPut(t, s, "a", "2")
	mustPut(t, s, "b", "3")
	docs := s.LiveDocs()
	var ids []string
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("LiveDocs 顺序 = %v", ids)
	}
}

func TestForwardHook(t *testing.T) {
	type ev struct {
		del  bool
		id   string
		body string
		seq  uint64
	}
	s := source.New()
	mustPut(t, s, "pre", "0") // seq 1，无钩子
	var got []ev
	s.Lock()
	snap := s.BeginForward(func(del bool, id string, body []byte, seq uint64) {
		got = append(got, ev{del, id, string(body), seq})
	})
	s.Unlock()
	if len(snap) != 1 || snap[0].ID != "pre" || snap[0].Seq != 1 {
		t.Fatalf("快照 = %+v", snap)
	}
	mustPut(t, s, "a", "1")                    // seq 2
	if _, err := s.Delete("pre"); err != nil { // seq 3
		t.Fatal(err)
	}
	s.Lock()
	s.EndForward(false)
	s.Unlock()
	mustPut(t, s, "b", "2") // seq 4，不再转发
	want := []ev{{false, "a", "1", 2}, {true, "pre", "", 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("双写事件 = %+v, want %+v", got, want)
	}
}
