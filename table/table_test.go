package table

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/handle"
)

func (t *Table) forceGen(idx int, gen uint32) { t.slots[idx].Gen = gen } // 测试钩子：推代号到上限

func TestInvalidCapacity(t *testing.T) {
	for _, capN := range []int{0, -1, -100, handle.MaxIndex + 2} {
		if _, err := New(capN); !errors.Is(err, ErrInvalidCapacity) {
			t.Errorf("New(%d) err = %v, want ErrInvalidCapacity", capN, err)
		}
	}
}

func TestHandleErrors(t *testing.T) {
	a, _ := New(4)
	b, _ := New(4)
	stale, _ := a.Insert("x")
	a.Remove(stale)
	foreign, _ := b.Insert("y")
	cases := []struct {
		name string
		h    handle.Handle
		want error
	}{
		{"zero", handle.Zero, ErrZeroHandle},
		{"foreign", foreign, ErrForeignHandle},
		{"stale", stale, ErrStaleHandle},
		{"stale-second-use", stale, ErrStaleHandle}, // 同一失效句柄再用，错误必须相同
	}
	for _, c := range cases {
		if _, err := a.Get(c.h); !errors.Is(err, c.want) {
			t.Errorf("%s: Get err = %v, want %v", c.name, err, c.want)
		}
		if err := a.Remove(c.h); !errors.Is(err, c.want) {
			t.Errorf("%s: Remove err = %v, want %v", c.name, err, c.want)
		}
	}
	if got := a.Len(); got != 0 {
		t.Errorf("Len = %d, want 0 (被拒操作不得留痕)", got)
	}
}

func TestStaleNeverRevivesAcrossReuse(t *testing.T) {
	tab, _ := New(1)
	old, _ := tab.Insert(0)
	tab.Remove(old)
	for i := 0; i < 500; i++ {
		h, _ := tab.Insert(i)
		tab.Remove(h)
		if _, err := tab.Get(old); !errors.Is(err, ErrStaleHandle) {
			t.Fatalf("reuse %d: Get(old) err = %v, want ErrStaleHandle", i, err)
		}
	}
}

func TestFullInsertKeepsState(t *testing.T) {
	tab, _ := New(2)
	h1, _ := tab.Insert(1)
	tab.Insert(2)
	beforeSlots, beforeFree := tab.Slots(), tab.FreeIndices()
	if _, err := tab.Insert(3); !errors.Is(err, ErrFull) {
		t.Fatalf("Insert err = %v, want ErrFull", err)
	}
	if got := tab.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	if !reflect.DeepEqual(beforeSlots, tab.Slots()) || !reflect.DeepEqual(beforeFree, tab.FreeIndices()) {
		t.Fatal("rejected Insert changed observable state")
	}
	tab.Remove(h1)
	if _, err := tab.Insert(3); err != nil {
		t.Fatalf("Insert after Remove err = %v", err)
	}
}

func TestExhaustion(t *testing.T) {
	tab, _ := New(2)
	keep, _ := tab.Insert("keep")
	tmp, _ := tab.Insert("tmp")
	idx := int(tmp.Index())
	tab.Remove(tmp)
	tab.forceGen(idx, handle.MaxGen) // 人为把代号推到上限
	dying, _ := tab.Insert("dying")
	if got := dying.Gen(); got != handle.MaxGen {
		t.Fatalf("gen = %d, want MaxGen", got)
	}
	if err := tab.Remove(dying); err != nil {
		t.Fatal(err)
	}
	if info := tab.Slots()[idx]; !info.Exhausted || info.InUse {
		t.Fatal("slot should be exhausted and not in use")
	}
	if _, err := tab.Get(dying); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("Get(dying) err = %v, want ErrStaleHandle", err)
	}
	if _, err := tab.Insert("x"); !errors.Is(err, ErrFull) {
		t.Fatalf("Insert err = %v, want ErrFull", err)
	}
	if _, err := tab.Get(keep); err != nil {
		t.Fatal("live handle on other slot must stay valid")
	}
	tab.Remove(keep)
	if _, err := tab.Insert("y"); err != nil {
		t.Fatal("table must keep working after a slot exhausts")
	}
	if tab.Len()+len(tab.FreeIndices())+1 != tab.Cap() {
		t.Fatal("Len + free + exhausted != Cap")
	}
}

func TestInvariantEquation(t *testing.T) {
	tab, _ := New(64)
	rng := rand.New(rand.NewSource(42))
	var live []handle.Handle
	for step := 0; step < 2000; step++ {
		if rng.Intn(2) == 0 || len(live) == 0 {
			if h, err := tab.Insert(step); err == nil {
				live = append(live, h)
			}
		} else {
			i := rng.Intn(len(live))
			if err := tab.Remove(live[i]); err != nil {
				t.Fatalf("Remove err = %v", err)
			}
			live = append(live[:i], live[i+1:]...)
		}
		liveN, deadN := 0, 0
		for _, s := range tab.Slots() {
			if s.InUse {
				liveN++
			}
			if s.Exhausted {
				deadN++
			}
		}
		if liveN != tab.Len() || liveN != len(live) ||
			liveN+len(tab.FreeIndices())+deadN != tab.Cap() {
			t.Fatalf("step %d: invariant violated (live=%d Len=%d tracked=%d)",
				step, liveN, tab.Len(), len(live))
		}
	}
}
