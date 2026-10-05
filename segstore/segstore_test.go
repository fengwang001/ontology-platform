package segstore_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/segstore"
)

func mkdocs(ids ...string) []segstore.Doc {
	out := make([]segstore.Doc, len(ids))
	for i, id := range ids {
		out[i] = segstore.Doc{ID: id, SortVal: int64(i)}
	}
	return out
}

func TestAddSegmentValidation(t *testing.T) {
	big := make([]segstore.Doc, 10001)
	for i := range big {
		big[i] = segstore.Doc{ID: fmt.Sprintf("d%05d", i)}
	}
	cases := []struct {
		name string
		now  int64
		docs []segstore.Doc
	}{
		{"now negative", -1, mkdocs("a")},
		{"now too large", segstore.MaxNow + 1, mkdocs("a")},
		{"empty batch", 0, nil},
		{"batch too large", 0, big},
		{"empty id", 0, []segstore.Doc{{ID: "", SortVal: 1}}},
		{"dup id in batch", 0, []segstore.Doc{{ID: "a", SortVal: 1}, {ID: "a", SortVal: 2}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := segstore.NewStore()
			if _, err := s.AddSegment(tc.now, tc.docs); !errors.Is(err, segstore.ErrInvalidParam) {
				t.Fatalf("err = %v, want ErrInvalidParam", err)
			}
			// 参数非法被拒不推进时钟：now=0 仍被接受。
			if _, err := s.AddSegment(0, mkdocs("x")); err != nil {
				t.Fatalf("clock advanced by rejected op: %v", err)
			}
		})
	}
}

func TestAddSegmentNumberingAndConflict(t *testing.T) {
	s := segstore.NewStore()
	n1, err := s.AddSegment(0, mkdocs("a", "b"))
	if err != nil || n1 != 1 {
		t.Fatalf("n1 = %d, err = %v", n1, err)
	}
	n2, err := s.AddSegment(1, mkdocs("c"))
	if err != nil || n2 != 2 {
		t.Fatalf("n2 = %d, err = %v", n2, err)
	}
	if _, err := s.AddSegment(2, mkdocs("a")); !errors.Is(err, segstore.ErrIDConflict) {
		t.Fatalf("err = %v, want ErrIDConflict", err)
	}
	// 被拒的 AddSegment 不占段号。
	n3, err := s.AddSegment(3, mkdocs("z"))
	if err != nil || n3 != 3 {
		t.Fatalf("n3 = %d, err = %v", n3, err)
	}
	// 删除后 id 不再存活，可重新入段。
	if err := s.Delete(4, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.AddSegment(5, mkdocs("a")); err != nil {
		t.Fatalf("re-add after delete: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := segstore.NewStore()
	if _, err := s.AddSegment(0, mkdocs("a", "b")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(1, "ghost"); !errors.Is(err, segstore.ErrDocNotFound) {
		t.Fatalf("err = %v, want ErrDocNotFound", err)
	}
	if err := s.Delete(2, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(3, "a"); !errors.Is(err, segstore.ErrDocNotFound) {
		t.Fatalf("double delete err = %v, want ErrDocNotFound", err)
	}
	// 参数非法优先于时钟回退。
	if err := s.Delete(-1, "b"); !errors.Is(err, segstore.ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam", err)
	}
}

func TestMergeValidation(t *testing.T) {
	s := segstore.NewStore()
	for i, id := range []string{"a", "b", "c"} {
		if _, err := s.AddSegment(int64(i), mkdocs(id)); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		segs []int64
		want error
	}{
		{"too few", []int64{1}, segstore.ErrInvalidParam},
		{"too many", []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, segstore.ErrInvalidParam},
		{"dup", []int64{1, 1}, segstore.ErrInvalidParam},
		{"missing", []int64{1, 99}, segstore.ErrSegNotFound},
		{"negative seg", []int64{1, -2}, segstore.ErrSegNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Merge(10, tc.segs); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMergeLifecycle(t *testing.T) {
	s := segstore.NewStore()
	if _, err := s.AddSegment(0, mkdocs("a", "b")); err != nil { // 段 1
		t.Fatal(err)
	}
	if _, err := s.AddSegment(1, mkdocs("c")); err != nil { // 段 2
		t.Fatal(err)
	}
	if _, err := s.AddSegment(2, mkdocs("d")); err != nil { // 段 3
		t.Fatal(err)
	}
	// 乱序传入，无 PIT 持有时旧段立即按段号升序释放。
	n, err := s.Merge(3, []int64{2, 1})
	if err != nil || n != 4 {
		t.Fatalf("merge = %d, err = %v", n, err)
	}
	if got := fmt.Sprint(s.Released()); got != "[1 2]" {
		t.Fatalf("released = %s, want [1 2]", got)
	}
	// 旧段已不在视图。
	if _, err := s.Merge(4, []int64{1, 4}); !errors.Is(err, segstore.ErrSegNotFound) {
		t.Fatalf("err = %v, want ErrSegNotFound", err)
	}
	// 被拒的合并不占段号。
	n2, err := s.Merge(5, []int64{3, 4})
	if err != nil || n2 != 5 {
		t.Fatalf("merge = %d, err = %v", n2, err)
	}
	if got := fmt.Sprint(s.Released()); got != "[1 2 3 4]" {
		t.Fatalf("released = %s, want [1 2 3 4]", got)
	}
}

func TestMergeAllDeletedCreatesNoSegment(t *testing.T) {
	s := segstore.NewStore()
	if _, err := s.AddSegment(0, mkdocs("a")); err != nil { // 段 1
		t.Fatal(err)
	}
	if _, err := s.AddSegment(1, mkdocs("b")); err != nil { // 段 2
		t.Fatal(err)
	}
	if err := s.Delete(2, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(3, "b"); err != nil {
		t.Fatal(err)
	}
	n, err := s.Merge(4, []int64{1, 2})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if n != 0 {
		t.Fatalf("merge = %d, want 0 (no live docs)", n)
	}
	if got := fmt.Sprint(s.Released()); got != "[1 2]" {
		t.Fatalf("released = %s, want [1 2]", got)
	}
	// 未占段号：下一段仍是 3。
	n2, err := s.AddSegment(5, mkdocs("c"))
	if err != nil || n2 != 3 {
		t.Fatalf("n2 = %d, err = %v, want 3", n2, err)
	}
}

func TestClockRules(t *testing.T) {
	s := segstore.NewStore()
	if _, err := s.AddSegment(10, mkdocs("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSegment(9, mkdocs("b")); !errors.Is(err, segstore.ErrClock) {
		t.Fatalf("err = %v, want ErrClock", err)
	}
	// now 相等允许。
	if _, err := s.AddSegment(10, mkdocs("b")); err != nil {
		t.Fatalf("equal now rejected: %v", err)
	}
	// 状态类被拒的操作也推进时钟。
	if err := s.Delete(20, "ghost"); !errors.Is(err, segstore.ErrDocNotFound) {
		t.Fatalf("err = %v, want ErrDocNotFound", err)
	}
	if _, err := s.AddSegment(19, mkdocs("c")); !errors.Is(err, segstore.ErrClock) {
		t.Fatalf("err = %v, want ErrClock", err)
	}
	// 参数非法被拒不推进时钟。
	if _, err := s.AddSegment(100, nil); !errors.Is(err, segstore.ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam", err)
	}
	if _, err := s.AddSegment(20, mkdocs("c")); err != nil {
		t.Fatalf("clock advanced by param-rejected op: %v", err)
	}
}

func TestConcurrencySmoke(t *testing.T) {
	s := segstore.NewStore()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				now := int64(i)
				id := fmt.Sprintf("w%dd%d", w, i)
				if _, err := s.AddSegment(now, mkdocs(id)); err != nil && !errors.Is(err, segstore.ErrClock) {
					t.Errorf("add: %v", err)
					return
				}
				if err := s.Delete(now, id); err != nil && !errors.Is(err, segstore.ErrClock) {
					t.Errorf("delete: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	seen := map[int64]bool{}
	for _, n := range s.Released() {
		if seen[n] {
			t.Errorf("released segment %d twice", n)
		}
		seen[n] = true
	}
}
