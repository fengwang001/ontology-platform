package ontology

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

// K <= 0 必须返回可判定的错误（errors.Is），而不是 panic。
func TestInvalidCapacity(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		sel, err := New(k, Desc)
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("k=%d: err = %v, want ErrInvalidCapacity", k, err)
		}
		if sel != nil {
			t.Fatalf("k=%d: selector must be nil on error", k)
		}
	}
}

// 非法方向同样返回可判定错误。
func TestInvalidDirection(t *testing.T) {
	if _, err := New(3, Direction(42)); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("err = %v, want ErrInvalidDirection", err)
	}
}

// 元素数不足 K 时 Snapshot 返回全部已有元素，并严格有序。
func TestSnapshotAllWhenUnderCapacity(t *testing.T) {
	sel, _ := New(10, Desc)
	sel.Push("a", 1)
	sel.Push("b", 4)
	sel.Push("c", 4)
	got := sel.Snapshot()
	if len(got) != 3 {
		t.Fatalf("len: got %d want 3", len(got))
	}
	if !reflect.DeepEqual(ids(got), []string{"b", "c", "a"}) {
		t.Fatalf("under-capacity order: %v", ids(got))
	}
}

// 长流上内部持有数在每次 Push 后都不得超过 K。
func TestHeldCountNeverExceedsK(t *testing.T) {
	const k = 8
	sel, _ := New(k, Desc)
	for i := 0; i < 10000; i++ {
		sel.Push(fmt.Sprintf("id-%05d", i), math.Sin(float64(i)))
		if n := sel.Len(); n > k {
			t.Fatalf("after %d pushes: held %d > k %d", i+1, n, k)
		}
	}
	if sel.Len() != k {
		t.Fatalf("final held: got %d want %d", sel.Len(), k)
	}
}

// Asc 方向的长流上界同样成立。
func TestHeldCountNeverExceedsKAsc(t *testing.T) {
	const k = 5
	sel, _ := New(k, Asc)
	for i := 0; i < 5000; i++ {
		sel.Push(fmt.Sprintf("n-%05d", i), float64((i*37)%101))
		if n := sel.Len(); n > k {
			t.Fatalf("held %d > k %d at push %d", n, k, i)
		}
	}
}

// Snapshot 返回值与内部状态互不影响：改返回切片不污染选择器。
func TestSnapshotIsolation(t *testing.T) {
	sel, _ := New(3, Desc)
	sel.Push("a", 1)
	sel.Push("b", 2)
	snap := sel.Snapshot()
	snap[0] = Element{ID: "tampered", Score: math.Inf(1)}
	snap = append(snap, Element{"extra", 9})
	_ = snap

	again := sel.Snapshot()
	if len(again) != 2 || again[0].ID != "b" || again[1].ID != "a" {
		t.Fatalf("internal state leaked through snapshot: %+v", again)
	}
}
