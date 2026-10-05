package search_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"ontology/pit"
	"ontology/search"
	"ontology/segstore"
)

func key(sortVal, seg, idx int64) search.Key {
	return search.Key{SortVal: sortVal, Seg: seg, Idx: idx}
}

func hitID(id string, sortVal, seg, idx int64) search.Hit {
	return search.Hit{ID: id, Key: key(sortVal, seg, idx)}
}

func mustAdd(t *testing.T, s *segstore.Store, now int64, docs ...segstore.Doc) int64 {
	t.Helper()
	n, err := s.AddSegment(now, docs)
	if err != nil {
		t.Fatalf("add segment: %v", err)
	}
	return n
}

func mustOpen(t *testing.T, m *pit.Manager, now, ka int64) int64 {
	t.Helper()
	pid, err := m.Open(now, ka)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return pid
}

func mustSearch(t *testing.T, sch *search.Searcher, now, pid int64, size int, after *search.Key, ka int64) []search.Hit {
	t.Helper()
	hits, err := sch.Search(now, pid, size, after, ka)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	return hits
}

// 题目中的完整实例：PIT 内看得见其后被删的 b 与被合并掉的段 1、2。
func TestSpecExample(t *testing.T) {
	s := segstore.NewStore()
	m := pit.NewManager(s, 2)
	sch := search.NewSearcher(s, m)

	if n := mustAdd(t, s, 0, segstore.Doc{ID: "a", SortVal: 5}, segstore.Doc{ID: "b", SortVal: 7}); n != 1 {
		t.Fatalf("seg = %d, want 1", n)
	}
	if n := mustAdd(t, s, 0, segstore.Doc{ID: "c", SortVal: 5}, segstore.Doc{ID: "d", SortVal: 9}); n != 2 {
		t.Fatalf("seg = %d, want 2", n)
	}
	pid := mustOpen(t, m, 10, 20) // PIT 1, exp=30
	if err := s.Delete(11, "b"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	n3, err := s.Merge(12, []int64{1, 2})
	if err != nil || n3 != 3 {
		t.Fatalf("merge = %d, err = %v", n3, err)
	}
	if got := s.Released(); len(got) != 0 {
		t.Fatalf("released = %v, want empty (pinned by PIT 1)", got)
	}

	hits := mustSearch(t, sch, 15, pid, 2, nil, 5) // exp=max(30,20)=30 不变
	want := []search.Hit{hitID("a", 5, 1, 0), hitID("c", 5, 2, 0)}
	if !slices.Equal(hits, want) {
		t.Fatalf("page1 = %v, want %v", hits, want)
	}

	after := hits[len(hits)-1].Key
	hits2 := mustSearch(t, sch, 15, pid, 2, &after, 0)
	want2 := []search.Hit{hitID("b", 7, 1, 1), hitID("d", 9, 2, 1)} // b 在 PIT 内仍可见
	if !slices.Equal(hits2, want2) {
		t.Fatalf("page2 = %v, want %v", hits2, want2)
	}

	hits3 := mustSearch(t, sch, 15, 0, 10, nil, 0)
	want3 := []search.Hit{hitID("a", 5, 3, 0), hitID("c", 5, 3, 1), hitID("d", 9, 3, 2)}
	if !slices.Equal(hits3, want3) {
		t.Fatalf("current view = %v, want %v", hits3, want3)
	}

	mustSearch(t, sch, 29, pid, 1, nil, 10) // exp 延到 max(30,39)=39
	mustSearch(t, sch, 38, pid, 1, nil, 0)  // 仍有效
	// t=39 恰等 exp：先落地过期，Released 追加 [1 2]，再报 PIT 不存在。
	if _, err := sch.Search(39, pid, 1, nil, 0); !errors.Is(err, pit.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := fmt.Sprint(s.Released()); got != "[1 2]" {
		t.Fatalf("released = %s, want [1 2]", got)
	}
	// 时钟已是 39。
	if err := s.Delete(38, "a"); !errors.Is(err, segstore.ErrClock) {
		t.Fatalf("err = %v, want ErrClock", err)
	}
}

// 同一实例改为 t=20 Close：Released 同样在那一刻追加 [1 2]。
func TestSpecExampleCloseVariant(t *testing.T) {
	s := segstore.NewStore()
	m := pit.NewManager(s, 2)
	mustAdd(t, s, 0, segstore.Doc{ID: "a", SortVal: 5}, segstore.Doc{ID: "b", SortVal: 7})
	mustAdd(t, s, 0, segstore.Doc{ID: "c", SortVal: 5}, segstore.Doc{ID: "d", SortVal: 9})
	pid := mustOpen(t, m, 10, 20)
	if err := s.Delete(11, "b"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Merge(12, []int64{1, 2}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := m.Close(20, pid); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := fmt.Sprint(s.Released()); got != "[1 2]" {
		t.Fatalf("released = %s, want [1 2]", got)
	}
}

// 同 sortVal 跨段时按段号、段内序号定序。
func TestCrossSegmentSameSortVal(t *testing.T) {
	s := segstore.NewStore()
	m := pit.NewManager(s, 4)
	sch := search.NewSearcher(s, m)
	mustAdd(t, s, 0, segstore.Doc{ID: "x", SortVal: 5})
	mustAdd(t, s, 1, segstore.Doc{ID: "y", SortVal: 5})
	mustAdd(t, s, 2, segstore.Doc{ID: "z", SortVal: 5})
	pid := mustOpen(t, m, 3, 1000)
	hits := mustSearch(t, sch, 4, pid, 10, nil, 0)
	want := []search.Hit{hitID("x", 5, 1, 0), hitID("y", 5, 2, 0), hitID("z", 5, 3, 0)}
	if !slices.Equal(hits, want) {
		t.Fatalf("hits = %v, want %v", hits, want)
	}
}

// 同一 PIT 以任意 size 切分翻页，拼接结果恒等于一次取尽。
func TestPaginationEquivalence(t *testing.T) {
	s := segstore.NewStore()
	m := pit.NewManager(s, 4)
	sch := search.NewSearcher(s, m)
	mustAdd(t, s, 0,
		segstore.Doc{ID: "a", SortVal: 1}, segstore.Doc{ID: "b", SortVal: 3},
		segstore.Doc{ID: "c", SortVal: 3}, segstore.Doc{ID: "d", SortVal: 5})
	mustAdd(t, s, 1,
		segstore.Doc{ID: "e", SortVal: 2}, segstore.Doc{ID: "f", SortVal: 3},
		segstore.Doc{ID: "g", SortVal: 4})
	mustAdd(t, s, 2,
		segstore.Doc{ID: "h", SortVal: 1}, segstore.Doc{ID: "i", SortVal: 5},
		segstore.Doc{ID: "j", SortVal: 6})
	pid := mustOpen(t, m, 3, 100000)
	// 开启后的写、删、合并不影响 PIT 内结果。
	if err := s.Delete(4, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Merge(5, []int64{2, 3}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	mustAdd(t, s, 6, segstore.Doc{ID: "k", SortVal: 1})

	full := mustSearch(t, sch, 7, pid, 1000, nil, 0)
	if len(full) != 10 {
		t.Fatalf("full = %d hits, want 10 (all docs visible in PIT)", len(full))
	}
	for size := 1; size <= 12; size++ {
		var got []search.Hit
		var after *search.Key
		for {
			hits := mustSearch(t, sch, 7, pid, size, after, 0)
			got = append(got, hits...)
			if len(hits) < size {
				break
			}
			k := hits[len(hits)-1].Key
			after = &k
		}
		if !slices.Equal(got, full) {
			t.Fatalf("size=%d: got %v, want %v", size, got, full)
		}
	}
}

// after 不要求是存在的键。
func TestAfterNeedNotExist(t *testing.T) {
	s := segstore.NewStore()
	m := pit.NewManager(s, 2)
	sch := search.NewSearcher(s, m)
	mustAdd(t, s, 0,
		segstore.Doc{ID: "a", SortVal: 1}, segstore.Doc{ID: "b", SortVal: 2},
		segstore.Doc{ID: "c", SortVal: 3})
	pid := mustOpen(t, m, 1, 1000)

	after := key(2, 1, 99) // 不存在的段内序号
	hits := mustSearch(t, sch, 2, pid, 10, &after, 0)
	if want := []search.Hit{hitID("c", 3, 1, 2)}; !slices.Equal(hits, want) {
		t.Fatalf("hits = %v, want %v", hits, want)
	}

	after2 := key(2, 9, 0) // 不存在的段号
	hits2 := mustSearch(t, sch, 2, pid, 10, &after2, 0)
	if want := []search.Hit{hitID("c", 3, 1, 2)}; !slices.Equal(hits2, want) {
		t.Fatalf("hits = %v, want %v", hits2, want)
	}

	after3 := key(3, 1, 2) // 恰为末键
	if hits3 := mustSearch(t, sch, 2, pid, 10, &after3, 0); len(hits3) != 0 {
		t.Fatalf("hits = %v, want empty", hits3)
	}
}

// pid=0 搜当前视图：after 必须为空且 ka 必须为 0；结果反映最新删除。
func TestCurrentViewConstraints(t *testing.T) {
	s := segstore.NewStore()
	m := pit.NewManager(s, 2)
	sch := search.NewSearcher(s, m)
	mustAdd(t, s, 0, segstore.Doc{ID: "a", SortVal: 1})

	after := key(1, 1, 0)
	if _, err := sch.Search(1, 0, 10, &after, 0); !errors.Is(err, segstore.ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam", err)
	}
	if _, err := sch.Search(1, 0, 10, nil, 5); !errors.Is(err, segstore.ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam", err)
	}
	hits := mustSearch(t, sch, 1, 0, 10, nil, 0)
	if want := []search.Hit{hitID("a", 1, 1, 0)}; !slices.Equal(hits, want) {
		t.Fatalf("hits = %v, want %v", hits, want)
	}
	if err := s.Delete(2, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if hits := mustSearch(t, sch, 3, 0, 10, nil, 0); len(hits) != 0 {
		t.Fatalf("hits = %v, want empty after delete", hits)
	}
}

// 续期只延不缩；exp 恰等过期、小 1 有效。
func TestRenewOnlyExtends(t *testing.T) {
	t.Run("no shrink", func(t *testing.T) {
		s := segstore.NewStore()
		m := pit.NewManager(s, 3)
		sch := search.NewSearcher(s, m)
		mustAdd(t, s, 0, segstore.Doc{ID: "a", SortVal: 1})
		// now+ka=52 < exp=101，过期时刻保持 101。
		pid := mustOpen(t, m, 1, 100)
		mustSearch(t, sch, 2, pid, 1, nil, 50)
		mustSearch(t, sch, 100, pid, 1, nil, 0) // exp-1 有效
		if _, err := sch.Search(101, pid, 1, nil, 0); !errors.Is(err, pit.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound (expired at exp)", err)
		}
	})
	t.Run("extend only", func(t *testing.T) {
		s := segstore.NewStore()
		m := pit.NewManager(s, 3)
		sch := search.NewSearcher(s, m)
		mustAdd(t, s, 0, segstore.Doc{ID: "a", SortVal: 1})
		// ka=10 把 exp 从 30 延到 39。
		pid := mustOpen(t, m, 10, 20)
		mustSearch(t, sch, 29, pid, 1, nil, 10)
		mustSearch(t, sch, 38, pid, 1, nil, 0)
		if _, err := sch.Search(39, pid, 1, nil, 0); !errors.Is(err, pit.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestSearchParamValidation(t *testing.T) {
	s := segstore.NewStore()
	m := pit.NewManager(s, 2)
	sch := search.NewSearcher(s, m)
	mustAdd(t, s, 0, segstore.Doc{ID: "a", SortVal: 1})
	pid := mustOpen(t, m, 1, 1000)

	cases := []struct {
		name     string
		now, pid int64
		size     int
		ka       int64
	}{
		{"size zero", 2, pid, 0, 0},
		{"size too large", 2, pid, 1001, 0},
		{"ka negative", 2, pid, 1, -1},
		{"ka too large", 2, pid, 1, pit.MaxKA + 1},
		{"pid negative", 2, -1, 1, 0},
		{"now negative", -1, pid, 1, 0},
		{"now too large", segstore.MaxNow + 1, pid, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := sch.Search(tc.now, tc.pid, tc.size, nil, tc.ka); !errors.Is(err, segstore.ErrInvalidParam) {
				t.Fatalf("err = %v, want ErrInvalidParam", err)
			}
		})
	}
}

func TestSearchPITNotFound(t *testing.T) {
	s := segstore.NewStore()
	m := pit.NewManager(s, 2)
	sch := search.NewSearcher(s, m)
	mustAdd(t, s, 0, segstore.Doc{ID: "a", SortVal: 1})
	if _, err := sch.Search(1, 999, 1, nil, 0); !errors.Is(err, pit.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	// 已关闭的 PIT 同样报不存在。
	pid := mustOpen(t, m, 2, 100)
	if err := m.Close(3, pid); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := sch.Search(4, pid, 1, nil, 0); !errors.Is(err, pit.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
