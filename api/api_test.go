package api

import (
	"fmt"
	"math/rand"
	"ontology/order"
	"ontology/rbuf"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func joinIDs(os []Out) (s string) {
	for _, o := range os {
		s += "," + o.ID
	}
	return strings.TrimPrefix(s, ",")
}
func TestSection3(t *testing.T) {
	r, _ := New(3, 10)
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	tss := []int64{5, 3, 9, 5, 6, 9, 4, 12, 12, 16}
	wantM := []string{"", "", "b,a", "", "", "", "", "c,f", "", "h,i"}
	wantS := []string{"", "", "", "d", "e", "", "g", "", "", ""}
	for i := range ids {
		m, s, err := r.Push(ids[i], tss[i])
		if err != nil || joinIDs(m) != wantM[i] || joinIDs(s) != wantS[i] {
			t.Fatalf("step %d: main=%q side=%q err=%v", i+1, joinIDs(m), joinIDs(s), err)
		}
	}
	if joinIDs(r.Flush()) != "j" || joinIDs(r.Main()) != "b,a,c,f,h,i,j" || joinIDs(r.Side()) != "d,e,g" {
		t.Fatalf("final: main=%v side=%v", r.Main(), r.Side())
	}
}
func TestNaiveReference(t *testing.T) {
	for _, n := range []int{1, 5, 50, 500} {
		for _, delay := range []int64{0, 3, 17} {
			rng := rand.New(rand.NewSource(int64(n)*100 + delay))
			ids := make([]string, n)
			tss := make([]int64, n)
			for i := range ids {
				ids[i] = fmt.Sprintf("e%d", i)
				tss[i] = int64(rng.Intn(3*n+1) - n)
			}
			if err := runReference(delay, n+1, ids, tss); err != nil {
				t.Fatalf("n=%d delay=%d: %v", n, delay, err)
			}
		}
	}
}
func TestBuffer(t *testing.T) {
	b := rbuf.New(4)
	in := []rbuf.Event{{ID: "c", TS: 9, Seq: 2}, {ID: "a", TS: 5, Seq: 0}, {ID: "b", TS: 5, Seq: 1}, {ID: "d", TS: 3, Seq: 3}}
	for _, e := range in {
		_ = b.Add(e) // 容量 4，不会失败
	}
	if got, want := b.Release(5), []rbuf.Event{in[3], in[1], in[2]}; !reflect.DeepEqual(got, want) || b.Len() != 1 {
		t.Fatalf("got %v len=%d", got, b.Len())
	}
	b2 := rbuf.New(1)
	_ = b2.Add(rbuf.Event{ID: "a", TS: 1, Seq: 0})
	if err := b2.Add(rbuf.Event{ID: "b", TS: 2, Seq: 1}); err != rbuf.ErrFull {
		t.Fatalf("err=%v", err)
	}
	if b2.Len() != 1 || len(b2.Release(9)) != 1 {
		t.Fatal("full add left a trace")
	}
}
func TestErrors(t *testing.T) {
	for _, c := range [][2]int64{{-1, 1}, {1, 0}} {
		if _, e := New(c[0], int(c[1])); e != ErrInvalidParam {
			t.Fatalf("params %v: %v", c, e)
		}
	}
	if d := map[error]int{ErrInvalidParam: 0, ErrEmptyID: 0, ErrDuplicateID: 0, ErrFull: 0}; len(d) != 4 {
		t.Fatal("sentinels not distinct")
	}
	r, _ := New(3, 1)
	_, _, _ = r.Push("x", 100)
	m0, s0 := r.Main(), r.Side()
	badIDs := []string{"", "x", "y"}
	badTS := []int64{1, 1, 1000}
	badErr := []error{ErrEmptyID, ErrDuplicateID, ErrFull}
	for i := range badIDs {
		if _, _, e := r.Push(badIDs[i], badTS[i]); e != badErr[i] {
			t.Fatalf("push(%q,%d): %v", badIDs[i], badTS[i], e)
		}
	}
	if !reflect.DeepEqual(r.Main(), m0) || !reflect.DeepEqual(r.Side(), s0) {
		t.Fatal("rejection left a trace")
	}
	if _, _, e := r.Push("z", 0); e != nil {
		t.Fatal("unusable after rejections")
	}
}
func TestConcurrent(t *testing.T) {
	const G, P = 8, 40
	r, _ := New(5, G*P)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Go(func() {
			for p := 0; p < P; p++ {
				if _, _, e := r.Push(fmt.Sprintf("g%dp%d", g, p), int64((g*P+p)*29%211)); e != nil {
					t.Error(e)
				}
			}
		})
	}
	wg.Wait()
	r.Flush()
	all := append(r.Main(), r.Side()...)
	if len(all) != G*P {
		t.Fatalf("outputs=%d want %d", len(all), G*P)
	}
	slices.SortFunc(all, func(a, b Out) int { return int(a.Seq - b.Seq) })
	for i, o := range all {
		if o.Seq != int64(i) {
			t.Fatalf("seq %d at position %d", o.Seq, i)
		}
	}
	refM, refS := naiveOutputs(5, all)
	sorted := slices.IsSortedFunc(r.Main(), func(a, b Out) int { return order.Compare(a.TS, a.Seq, b.TS, b.Seq) })
	if !reflect.DeepEqual(r.Main(), refM) || !reflect.DeepEqual(r.Side(), refS) || !sorted {
		t.Fatal("concurrent result violates invariants")
	}
}
func TestReleaseChecksBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		for _, k := range []int{0, 3} {
			b := rbuf.New(m + k + 1)
			for i := 0; i < m+k; i++ {
				ts := int64(1) << 40
				if i < k {
					ts = int64(i)
				}
				_ = b.Add(rbuf.Event{ID: fmt.Sprintf("e%d", i), TS: ts, Seq: int64(i)})
			}
			if out := b.Release(int64(k)); len(out) != k {
				t.Fatalf("m=%d k=%d: released %d", m, k, len(out))
			}
			f := reflect.ValueOf(b).Elem().FieldByName("checked")
			if n := int(reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int()); n > k+1 {
				t.Fatalf("m=%d k=%d: checked %d > %d", m, k, n, k+1)
			}
		}
	}
}
