package maglev

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// ---------- specification example ----------

func TestSpecExample_Lim7(t *testing.T) {
	tb, err := New(7, 7)
	if err != nil {
		t.Fatal(err)
	}

	// register A: empty table, all seven slots are mandatory -> all A
	n, err := tb.AddBackend("A", 3, 4, 1)
	if err != nil || n != 7 {
		t.Fatalf("add A: n=%d err=%v, want 7", n, err)
	}
	if got := strings.Join(tb.Table(), ""); got != "AAAAAAA" {
		t.Fatalf("after add A: %s", got)
	}

	// register B: target B,B,B,A,A,A,B; four optional slots differ
	n, err = tb.AddBackend("B", 0, 2, 2)
	if err != nil || n != 4 {
		t.Fatalf("add B: n=%d err=%v, want 4", n, err)
	}
	if got := strings.Join(tb.Table(), ""); got != "BBBAAAB" {
		t.Fatalf("after add B: %s, want BBBAAAB", got)
	}

	// register C: target B,A,B,A,C,B,B; slots 1,4,5 differ
	n, err = tb.AddBackend("C", 3, 1, 1)
	if err != nil || n != 3 {
		t.Fatalf("add C: n=%d err=%v, want 3", n, err)
	}
	if got := strings.Join(tb.Table(), ""); got != "BABACBB" {
		t.Fatalf("after add C: %s, want BABACBB", got)
	}
	if tb.Pending() != 0 {
		t.Fatalf("pending=%d, want 0", tb.Pending())
	}

	// remove C: slot 4 mandatory (C gone), slots 1 and 5 optional
	n, err = tb.RemoveBackend("C")
	if err != nil || n != 3 {
		t.Fatalf("remove C: n=%d err=%v, want 3", n, err)
	}
	if got := strings.Join(tb.Table(), ""); got != "BBBAAAB" {
		t.Fatalf("after remove C: %s, want BBBAAAB", got)
	}
	if tb.Pending() != 0 {
		t.Fatalf("pending=%d, want 0", tb.Pending())
	}
}

func TestSpecExample_Lim1(t *testing.T) {
	tb, err := New(7, 1)
	if err != nil {
		t.Fatal(err)
	}

	addOK(t, tb, "A", 3, 4, 1)
	for tb.Pending() != 0 {
		tb.Step()
	}
	addOK(t, tb, "B", 0, 2, 2)
	for tb.Pending() != 0 {
		tb.Step()
	}
	addOK(t, tb, "C", 3, 1, 1)
	for tb.Pending() != 0 {
		tb.Step()
	}
	if got := strings.Join(tb.Table(), ""); got != "BABACBB" {
		t.Fatalf("converged: %s, want BABACBB", got)
	}

	// remove C: mandatory slot 4 -> A, then one optional ascending -> slot 1 -> B
	n, err := tb.RemoveBackend("C")
	if err != nil || n != 2 {
		t.Fatalf("remove C: n=%d err=%v, want 2", n, err)
	}
	if got := strings.Join(tb.Table(), ""); got != "BBBAABB" {
		t.Fatalf("after remove C: %s, want BBBAABB", got)
	}
	if tb.Pending() != 1 {
		t.Fatalf("pending=%d, want 1", tb.Pending())
	}

	if n := tb.Step(); n != 1 {
		t.Fatalf("step n=%d, want 1", n)
	}
	if got := strings.Join(tb.Table(), ""); got != "BBBAAAB" {
		t.Fatalf("after step: %s, want BBBAAAB", got)
	}
	if tb.Pending() != 0 {
		t.Fatalf("pending=%d, want 0", tb.Pending())
	}

	if n := tb.Step(); n != 0 {
		t.Fatalf("idle step n=%d, want 0", n)
	}
}

func addOK(t *testing.T, tb *Table, name string, offset, skip, weight int) {
	t.Helper()
	if _, err := tb.AddBackend(name, offset, skip, weight); err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
}

// ---------- target-table filling rules ----------

// Weight>1 backend claims several slots per turn; pointers persist across
// rounds without resetting to 0; occupied permutation slots are skipped.
func TestWeightedTurnsPersistentPointers(t *testing.T) {
	// M=5, A(0,1,w1) perm 0,1,2,3,4; B(0,2,w2) perm 0,2,4,1,3.
	// round 1 (A first by name): A claims 0 (ptr 1); B skips 0, claims 2,4
	//   (ptr 3).
	// round 2: A skips 1? ptr1 -> slot1 empty, claims 1 (ptr 2); B ptr3 ->
	//   slot1 occupied, skip, claims 3 -> table full.
	tb, _ := New(5, 5)
	addOK(t, tb, "B", 0, 2, 2)
	addOK(t, tb, "A", 0, 1, 1)
	for tb.Step(); tb.Pending() != 0; tb.Step() {
	}
	if got := strings.Join(tb.Table(), ""); got != "AABBB" {
		t.Fatalf("got %s, want AABBB", got)
	}
}

// Table fills in the middle of the first backend's turn; later backends never
// place anything during that round.
func TestFillStopsMidTurn(t *testing.T) {
	tb, _ := New(3, 3)
	addOK(t, tb, "A", 0, 1, 3)
	addOK(t, tb, "B", 1, 1, 3)
	addOK(t, tb, "C", 2, 1, 3)
	for tb.Step(); tb.Pending() != 0; tb.Step() {
	}
	if got := strings.Join(tb.Table(), ""); got != "AAA" {
		t.Fatalf("got %s, want AAA", got)
	}
}

// A backend's turn is truncated exactly at the slot that fills the table.
func TestTurnTruncatedAtFullSlot(t *testing.T) {
	// M=5, A(1,1,w4), B(0,1,w2).
	// Turns run in name order A then B: A's first turn claims 1,2,3,4; B's
	// turn starts at 0 and its first claimed slot fills the table, truncating
	// the remainder of B's weight-2 turn.
	tb, _ := New(5, 5)
	addOK(t, tb, "B", 0, 1, 2)
	addOK(t, tb, "A", 1, 1, 4)
	for tb.Step(); tb.Pending() != 0; tb.Step() {
	}
	if got := strings.Join(tb.Table(), ""); got != "BAAAA" {
		t.Fatalf("got %s, want BAAAA (B's turn truncated at filling slot 0)", got)
	}
}

// Target depends on name byte order, not on registration order.
func TestNameOrderIndependentOfRegistration(t *testing.T) {
	build := func(order ...string) []string {
		tb, _ := New(11, 11)
		for _, name := range order {
			off := int(name[0]-'A') + 1
			addOK(t, tb, name, off, 1, 2)
		}
		for tb.Step(); tb.Pending() != 0; tb.Step() {
		}
		return tb.Table()
	}
	a := build("C", "A", "B")
	b := build("B", "C", "A")
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("target depends on registration order:\n%v\n%v", a, b)
	}
}

// A single backend fills the whole table.
func TestSingleBackendFillsAll(t *testing.T) {
	tb, _ := New(13, 13)
	n, err := tb.AddBackend("only", 7, 5, 16)
	if err != nil || n != 13 {
		t.Fatalf("n=%d err=%v, want 13", n, err)
	}
	for _, s := range tb.Table() {
		if s != "only" {
			t.Fatalf("table %v not all only", tb.Table())
		}
	}
	if name, err := tb.Lookup(1000000); err != nil || name != "only" {
		t.Fatalf("lookup=%s err=%v", name, err)
	}
}

// M=2, backend count exactly M: each backend owns one slot; further adds fail.
func TestM2_BackendCountEqualsM(t *testing.T) {
	tb, err := New(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := tb.AddBackend("A", 0, 1, 1); err != nil || n != 2 {
		t.Fatalf("add A n=%d err=%v, want 2", n, err)
	}
	if n, err := tb.AddBackend("B", 1, 1, 1); err != nil || n != 1 {
		t.Fatalf("add B n=%d err=%v, want 1", n, err)
	}
	if got := tb.Table(); got[0] != "A" || got[1] != "B" {
		t.Fatalf("got %v, want [A B]", got)
	}
	if _, err := tb.AddBackend("C", 0, 1, 1); !errors.Is(err, ErrTableFull) {
		t.Fatalf("err=%v, want ErrTableFull", err)
	}
}

// ---------- migration rules ----------

// Mandatory slots ignore Lim and are applied before optional slots.
func TestMandatorySlotsIgnoreLimAndGoFirst(t *testing.T) {
	tb, _ := New(7, 1)
	addOK(t, tb, "A", 3, 4, 1)
	for tb.Pending() != 0 {
		tb.Step()
	}
	addOK(t, tb, "B", 0, 2, 2)
	for tb.Pending() != 0 {
		tb.Step()
	}
	addOK(t, tb, "C", 3, 1, 1)
	for tb.Pending() != 0 {
		tb.Step()
	}
	// cur BABACBB -> target BBBAAAB: mandatory slot4 (C->A), optional slot1 (A->B)
	n, err := tb.RemoveBackend("C")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v, want 2 = 1 mandatory + 1 optional despite lim=1", n, err)
	}
	if got := strings.Join(tb.Table(), ""); got != "BBBAABB" {
		t.Fatalf("got %s, want BBBAABB", got)
	}
}

// Optional slots are applied in ascending slot order, at most Lim per call.
func TestOptionalSlotsAscendingAndLimited(t *testing.T) {
	tb, _ := New(7, 1)
	addOK(t, tb, "A", 3, 4, 1)
	addOK(t, tb, "B", 0, 2, 2)
	// add B on all-A: target BBBAAAB, optional diffs at 0,1,2,6; only slot0 now
	if got := strings.Join(tb.Table(), ""); got != "BAAAAAA" {
		t.Fatalf("got %s, want BAAAAAA", got)
	}
	if n := tb.Step(); n != 1 {
		t.Fatalf("step n=%d, want 1", n)
	}
	if got := strings.Join(tb.Table(), ""); got != "BBAAAAA" {
		t.Fatalf("got %s, want BBAAAAA", got)
	}
	tb.Step() // slot2
	if got := strings.Join(tb.Table(), ""); got != "BBBAAAA" {
		t.Fatalf("got %s, want BBBAAAA", got)
	}
	tb.Step() // slots 3,4,5 already match; slot6 changes
	if got := strings.Join(tb.Table(), ""); got != "BBBAAAB" {
		t.Fatalf("got %s, want BBBAAAB", got)
	}
	if n := tb.Step(); n != 0 || tb.Pending() != 0 {
		t.Fatalf("idle: n=%d pending=%d", n, tb.Pending())
	}
}

// Every Step reduces Pending by exactly min(Lim, Pending); convergence within
// ceil(M/Lim) steps.
func TestStepDecreasesPendingExactly(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 7))
	const M, lim = 53, 5
	tb, _ := New(M, lim)
	for k := 0; k < 7; k++ {
		name := fmt.Sprintf("be%02d", k)
		if _, err := tb.AddBackend(name, rng.IntN(M), 1+rng.IntN(M-1), 1+rng.IntN(16)); err != nil {
			t.Fatal(err)
		}
	}
	steps := 0
	for p := tb.Pending(); p > 0; p = tb.Pending() {
		want := p
		if want > lim {
			want = lim
		}
		if n := tb.Step(); n != want {
			t.Fatalf("step changed %d, want %d (pending was %d)", n, want, p)
		}
		if tb.Pending() != p-want {
			t.Fatalf("pending after step = %d, want %d", tb.Pending(), p-want)
		}
		steps++
		if steps > (M+lim-1)/lim {
			t.Fatalf("too many steps: %d", steps)
		}
	}
}

// A mutation while migration is incomplete reclassifies against the new T*.
func TestMutationMidMigration(t *testing.T) {
	tb, _ := New(7, 1)
	addOK(t, tb, "A", 3, 4, 1)
	addOK(t, tb, "B", 0, 2, 2)
	// cur BAAAAAA; add C -> target BABACBB; no mandatory slots, optional
	// ascending: slot1 A->B
	n, err := tb.AddBackend("C", 3, 1, 1)
	if err != nil || n != 1 {
		t.Fatalf("add C mid migration n=%d err=%v, want 1", n, err)
	}
	if got := strings.Join(tb.Table(), ""); got != "BABAAAA" {
		t.Fatalf("got %s, want BABAAAA", got)
	}
	// pending is measured against the new target: slots 4, 5 and 6 remain
	if tb.Pending() != 3 {
		t.Fatalf("pending=%d, want 3", tb.Pending())
	}
}

// Empty -> register and remove-to-empty both change exactly M slots; after
// removing the last backend the table is empty and Lookup reports no backends.
func TestEmptyTransitions(t *testing.T) {
	tb, _ := New(7, 2)
	n, err := tb.AddBackend("A", 3, 4, 1)
	if err != nil || n != 7 {
		t.Fatalf("register on empty n=%d err=%v, want 7", n, err)
	}
	n, err = tb.RemoveBackend("A")
	if err != nil || n != 7 {
		t.Fatalf("remove last n=%d err=%v, want 7", n, err)
	}
	for _, s := range tb.Table() {
		if s != "" {
			t.Fatalf("cur not empty: %v", tb.Table())
		}
	}
	if tb.Pending() != 0 {
		t.Fatalf("pending=%d, want 0", tb.Pending())
	}
	if n := tb.Step(); n != 0 {
		t.Fatalf("step with no backends n=%d, want 0", n)
	}
	if _, err := tb.Lookup(0); !errors.Is(err, ErrNoBackends) {
		t.Fatalf("lookup err=%v, want ErrNoBackends", err)
	}
}

// Lim=M migrates everything in one call.
func TestLimM_OneShot(t *testing.T) {
	tb, _ := New(31, 31)
	addOK(t, tb, "a", 5, 7, 3)
	addOK(t, tb, "b", 1, 3, 2)
	addOK(t, tb, "c", 9, 11, 5)
	addOK(t, tb, "d", 20, 13, 1)
	if tb.Pending() != 0 {
		t.Fatalf("pending=%d, want 0 with lim=M", tb.Pending())
	}
	n, err := tb.RemoveBackend("b")
	if err != nil || n == 0 || tb.Pending() != 0 {
		t.Fatalf("remove b n=%d err=%v pending=%d", n, err, tb.Pending())
	}
}

// ---------- rejection rules ----------

func TestInvalidConfig(t *testing.T) {
	for _, c := range [][2]int{
		{1, 1}, {4, 1}, {65537, 0}, {65537, 65538}, {65536, 1}, {100, 50},
		{3, -1}, {2, 3},
	} {
		if tb, err := New(c[0], c[1]); err == nil {
			t.Fatalf("New(%d,%d) = %v, want error", c[0], c[1], tb)
		}
	}
	for _, c := range [][2]int{{2, 1}, {3, 3}, {65537, 65537}, {65537, 1}} {
		if _, err := New(c[0], c[1]); err != nil {
			t.Fatalf("New(%d,%d): %v", c[0], c[1], err)
		}
	}
}

func TestAddBackendRejectionOrderAndNoStateChange(t *testing.T) {
	tb, _ := New(3, 1)
	snap := tb.Table()

	// 1) empty name beats out-of-range and everything else
	if _, err := tb.AddBackend("", 9, 9, 9); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("err=%v, want ErrEmptyName", err)
	}
	// 2) range beats duplicate beats full
	if _, err := tb.AddBackend("A", 3, 1, 1); !errors.Is(err, ErrInvalidBackend) {
		t.Fatalf("offset=M err=%v, want ErrInvalidBackend", err)
	}
	if _, err := tb.AddBackend("A", 0, 0, 1); !errors.Is(err, ErrInvalidBackend) {
		t.Fatalf("skip=0 err=%v, want ErrInvalidBackend", err)
	}
	if _, err := tb.AddBackend("A", 0, 3, 1); !errors.Is(err, ErrInvalidBackend) {
		t.Fatalf("skip=M err=%v, want ErrInvalidBackend", err)
	}
	if _, err := tb.AddBackend("A", 0, 1, 0); !errors.Is(err, ErrInvalidBackend) {
		t.Fatalf("weight=0 err=%v, want ErrInvalidBackend", err)
	}
	if _, err := tb.AddBackend("A", -1, 1, 17); !errors.Is(err, ErrInvalidBackend) {
		t.Fatalf("range err=%v", err)
	}

	addOK(t, tb, "A", 0, 1, 1)
	if _, err := tb.AddBackend("A", 0, 1, 1); !errors.Is(err, ErrBackendExists) {
		t.Fatalf("dup err=%v, want ErrBackendExists", err)
	}
	addOK(t, tb, "B", 1, 1, 1)
	addOK(t, tb, "C", 2, 1, 1)
	if _, err := tb.AddBackend("D", 0, 1, 1); !errors.Is(err, ErrTableFull) {
		t.Fatalf("full err=%v, want ErrTableFull", err)
	}
	if _, err := tb.RemoveBackend("ghost"); !errors.Is(err, ErrBackendNotFound) {
		t.Fatalf("missing remove err=%v, want ErrBackendNotFound", err)
	}

	// rejected ops changed neither backend set nor cur
	if got := tb.Table(); len(got) != 3 || !reflect.DeepEqual(got[0:0], snap[0:0]) {
		t.Fatalf("state changed by rejected ops: %v", got)
	}
	count := 0
	for _, s := range tb.Table() {
		if s != "" {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("filled slots=%d, want 3", count)
	}
}

// Table returns a copy: mutating it does not affect the lookup table.
func TestTableReturnsCopy(t *testing.T) {
	tb, _ := New(5, 5)
	addOK(t, tb, "A", 0, 1, 1)
	cp := tb.Table()
	cp[0] = "tampered"
	if name, _ := tb.Lookup(0); name != "A" {
		t.Fatalf("internal table mutated: lookup=%s", name)
	}
}

// ---------- concurrency ----------

func TestConcurrentAccess(t *testing.T) {
	tb, err := New(97, 7)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	names := []string{"a", "b", "c", "d", "e"}
	for k, name := range names {
		wg.Add(1)
		go func(k int, name string) {
			defer wg.Done()
			_, _ = tb.AddBackend(name, k*13, 1+k*3, 1+(k%16))
		}(k, name)
	}
	for k := 0; k < 4; k++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tb.Step()
				_ = tb.Pending()
				if name, err := tb.Lookup(seed + uint64(i)); err == nil {
					if name == "" {
						t.Errorf("empty owner returned for live set")
						return
					}
				}
				_ = tb.Table()
			}
		}(uint64(k * 1000))
	}
	wg.Wait()
	for tb.Pending() != 0 {
		tb.Step()
	}
	cur := tb.Table()
	live := map[string]bool{}
	for _, name := range names {
		live[name] = true
	}
	for s, owner := range cur {
		if !live[owner] {
			t.Fatalf("slot %d owned by removed/unknown backend %q", s, owner)
		}
	}
}
