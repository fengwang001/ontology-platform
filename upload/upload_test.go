package upload

import (
	"errors"
	"testing"
)

// 语义 1：分片号边界，非法号不改变任何计数。
func TestPartNumberBounds(t *testing.T) {
	u := New(3, 10)
	for _, n := range []int{-1, 0, 4, 100} {
		if err := u.Put(n, 10, "e"); !errors.Is(err, ErrBadPart) {
			t.Fatalf("Put(%d) = %v, want ErrBadPart", n, err)
		}
	}
	if r := u.Stat(); r.Received != 0 || r.Bytes != 0 || r.Replaced != 0 {
		t.Fatalf("invalid Put changed counters: %+v", r)
	}
	if err := u.Put(1, 10, "a"); err != nil {
		t.Fatalf("Put(1) = %v", err)
	}
	if err := u.Put(3, 1, "c"); err != nil {
		t.Fatalf("Put(total) = %v", err)
	}
	if r := u.Stat(); r.Received != 2 || r.Bytes != 11 {
		t.Fatalf("boundary parts not counted: %+v", r)
	}
}

// 语义 2：最小片大小，最后一片不受限，空片一律非法。
func TestMinPartSize(t *testing.T) {
	u := New(3, 10)
	if err := u.Put(1, 9, "a"); !errors.Is(err, ErrBadPart) {
		t.Fatalf("small non-last part = %v, want ErrBadPart", err)
	}
	if err := u.Put(2, 0, "b"); !errors.Is(err, ErrBadPart) {
		t.Fatalf("zero-size part = %v, want ErrBadPart", err)
	}
	if err := u.Put(3, 0, "c"); !errors.Is(err, ErrBadPart) {
		t.Fatalf("zero-size last part = %v, want ErrBadPart", err)
	}
	if err := u.Put(3, 1, "c"); err != nil {
		t.Fatalf("tiny last part = %v, want nil", err)
	}
	if err := u.Put(1, 10, "a"); err != nil {
		t.Fatalf("exact min part = %v, want nil", err)
	}
	if r := u.Stat(); r.Received != 2 || r.Bytes != 11 || r.Replaced != 0 {
		t.Fatalf("bad size Put changed counters: %+v", r)
	}
}

// 语义 3：覆盖的账目。
func TestReplaceAccounting(t *testing.T) {
	u := New(2, 5)
	mustPut(t, u, 1, 5, "a")
	mustPut(t, u, 2, 7, "b")
	mustPut(t, u, 1, 20, "a2")
	mustPut(t, u, 1, 8, "a3")
	r := u.Stat()
	if r.Received != 2 {
		t.Fatalf("Received = %d, want 2", r.Received)
	}
	if r.Bytes != 15 {
		t.Fatalf("Bytes = %d, want 15 (part1=8, part2=7)", r.Bytes)
	}
	if r.Replaced != 2 {
		t.Fatalf("Replaced = %d, want 2", r.Replaced)
	}
}

// 语义 4：缺片报告完整且升序，失败不标记完成。
func TestMissingPartsReport(t *testing.T) {
	u := New(5, 1)
	mustPut(t, u, 2, 1, "b")
	mustPut(t, u, 4, 1, "d")
	if err := u.Complete([]string{"a", "b", "c", "d", "e"}); !errors.Is(err, ErrMissingPart) {
		t.Fatalf("Complete = %v, want ErrMissingPart", err)
	}
	want := []int{1, 3, 5}
	got := u.Stat().Missing
	if len(got) != len(want) {
		t.Fatalf("Missing = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Missing = %v, want %v", got, want)
		}
	}
	// 缺片失败后可补传再完成。
	mustPut(t, u, 1, 1, "a")
	mustPut(t, u, 3, 1, "c")
	mustPut(t, u, 5, 1, "e")
	if err := u.Complete([]string{"a", "b", "c", "d", "e"}); err != nil {
		t.Fatalf("Complete after fill = %v, want nil", err)
	}
	if m := u.Stat().Missing; len(m) != 0 {
		t.Fatalf("Missing after success = %v, want empty", m)
	}
}

// 语义 5：etag 比对，失败不丢已登记分片。
func TestEtagMismatch(t *testing.T) {
	u := New(2, 1)
	mustPut(t, u, 1, 3, "a")
	mustPut(t, u, 2, 4, "b")
	if err := u.Complete([]string{"a"}); !errors.Is(err, ErrEtagMismatch) {
		t.Fatalf("short etags = %v, want ErrEtagMismatch", err)
	}
	if err := u.Complete([]string{"a", "x"}); !errors.Is(err, ErrEtagMismatch) {
		t.Fatalf("wrong etag = %v, want ErrEtagMismatch", err)
	}
	r := u.Stat()
	if r.Received != 2 || r.Bytes != 7 {
		t.Fatalf("mismatch dropped parts: %+v", r)
	}
	mustPut(t, u, 2, 4, "b") // 重传修正后再完成
	if err := u.Complete([]string{"a", "b"}); err != nil {
		t.Fatalf("Complete after fix = %v, want nil", err)
	}
}

// 语义 6：完成后冻结。
func TestFrozenAfterComplete(t *testing.T) {
	u := New(1, 1)
	mustPut(t, u, 1, 5, "a")
	if err := u.Complete([]string{"a"}); err != nil {
		t.Fatalf("Complete = %v", err)
	}
	if err := u.Put(1, 9, "z"); !errors.Is(err, ErrCompleted) {
		t.Fatalf("Put after complete = %v, want ErrCompleted", err)
	}
	if err := u.Complete([]string{"a"}); !errors.Is(err, ErrCompleted) {
		t.Fatalf("second Complete = %v, want ErrCompleted", err)
	}
	r := u.Stat()
	if r.Received != 1 || r.Bytes != 5 || r.Replaced != 0 {
		t.Fatalf("frozen stat changed: %+v", r)
	}
}

// 语义 7：缺片与 etag 数量同时错误时，先报 ErrMissingPart。
func TestErrorPriority(t *testing.T) {
	u := New(3, 1)
	mustPut(t, u, 1, 1, "a")
	err := u.Complete([]string{"a", "b"}) // 缺 2、3 且长度也不对
	if !errors.Is(err, ErrMissingPart) {
		t.Fatalf("Complete = %v, want ErrMissingPart first", err)
	}
}

func mustPut(t *testing.T, u *Upload, n int, size int64, etag string) {
	t.Helper()
	if err := u.Put(n, size, etag); err != nil {
		t.Fatalf("Put(%d) = %v, want nil", n, err)
	}
}
