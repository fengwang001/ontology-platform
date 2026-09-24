package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/join"
)

func pi(v int64) *int64 { return &v }

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestRightRetainedAfterDelL 钉住不变量 3：DelL 只撤回行，右记录留存供再插入复用。
func TestRightRetainedAfterDelL(t *testing.T) {
	v := New()
	if _, err := v.PutL("k1", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := v.PutR("k1", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := v.DelL("k1"); err != nil {
		t.Fatal(err)
	}
	ops, err := v.PutL("k1", 15)
	if err != nil {
		t.Fatal(err)
	}
	wantOps := []Op{join.Op{Insert: true, K: "k1", LV: 15, RV: pi(100)}}
	if !reflect.DeepEqual(ops, wantOps) {
		t.Fatalf("ops %v != %v", ops, wantOps)
	}
	want := []Row{join.Row{K: "k1", LV: 15, RV: pi(100)}}
	if got := v.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("view %v != %v", got, want)
	}
}

// TestErrorsDistinct 钉住三类可判定错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	sents := []error{ErrEmptyKey, ErrNoLeft, ErrNoRight}
	for i, a := range sents {
		for j, b := range sents {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinel %d and %d not distinct", i, j)
			}
		}
	}
}

// TestFailureAtomic 钉住不变量 4：被拒操作不改宽表/右集/日志，之后仍可正常使用。
func TestFailureAtomic(t *testing.T) {
	cases := []struct {
		name  string
		setup func(v *View)
		call  func(v *View) error
		want  error
	}{
		{"empty PutL", func(v *View) {}, func(v *View) error { _, e := v.PutL("", 1); return e }, ErrEmptyKey},
		{"empty PutR", func(v *View) {}, func(v *View) error { _, e := v.PutR("", 1); return e }, ErrEmptyKey},
		{"DelL missing", func(v *View) {}, func(v *View) error { _, e := v.DelL("x"); return e }, ErrNoLeft},
		{"DelR missing", func(v *View) {}, func(v *View) error { _, e := v.DelR("x"); return e }, ErrNoRight},
		{"DelR absent RV", func(v *View) { _, _ = v.PutL("x", 1) }, func(v *View) error { _, e := v.DelR("x"); return e }, ErrNoRight},
		{"DelL twice", func(v *View) { _, _ = v.PutL("x", 1); _, _ = v.DelL("x") }, func(v *View) error { _, e := v.DelL("x"); return e }, ErrNoLeft},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := New()
			c.setup(v)
			before := v.View()
			if err := c.call(v); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if got := v.View(); !reflect.DeepEqual(got, before) {
				t.Fatalf("view changed after rejection: %v != %v", got, before)
			}
			if _, err := v.PutL("z", 9); err != nil {
				t.Fatalf("not usable after rejection: %v", err)
			}
		})
	}
}

// TestFailureRightUntouched：DelR 被拒时右记录集本身也不得改变。
func TestFailureRightUntouched(t *testing.T) {
	v := join.New()
	_, _ = v.PutL("x", 1) // 无右记录，DelR 必被拒
	before := v.Rights()
	if _, err := v.DelR("x"); !errors.Is(err, join.ErrNoRight) {
		t.Fatalf("got %v", err)
	}
	if got := v.Rights(); !reflect.DeepEqual(got, before) {
		t.Fatalf("rights changed: %v != %v", got, before)
	}
}

// TestConcurrentReaders：N 个 goroutine 并发只读 View，快照逐行逐字段一致；
// 另一 goroutine 并发做不改变宽表的合法 PutL/PutR（同值重写、无左右记录）。
func TestConcurrentReaders(t *testing.T) {
	v := New()
	const m = 500
	for i := 0; i < m; i++ {
		_, _ = v.PutL(fmt.Sprintf("c%04d", i), int64(i))
	}
	want := v.View()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got := v.View(); !reflect.DeepEqual(got, want) {
					t.Errorf("snapshot diverged: %d rows", len(got))
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() { // 合法但不改变宽表的写
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			_, _ = v.PutL("c0000", 0)
			_, _ = v.PutR(fmt.Sprintf("orphan%04d", i&15), int64(i))
		}
		close(stop)
	}()
	wg.Wait()
	if got := v.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("final snapshot diverged: %d rows", len(got))
	}
}
