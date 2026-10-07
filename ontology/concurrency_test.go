package ontology

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentReadsAndWrites hammers one instance from many goroutines.
// Every accepted write replaces salary with its own constant delta, so any
// in-flight read observes exactly one serial state; the final version and
// timestamp reflect precisely the number of accepted writes.
func TestConcurrentReadsAndWrites(t *testing.T) {
	al, _ := allowDeny()
	cfg := Config{
		RowMode: AllowOverrides, PropertyMode: AllowOverrides,
		DefaultRow: EffectAllow, DefaultRead: EffectAllow, DefaultWrite: EffectAllow,
		WriteMode: WriteReject,
	}
	h := newHarness(t, cfg)
	h.store.Put(Instance{
		Type:    empType,
		ID:      "c1",
		Values:  map[string]Value{"salary": {Int: 0}},
		Present: map[string]bool{"salary": true},
	})
	var clockNanos int64
	h.store.SetClock(func() int64 { return atomic.AddInt64(&clockNanos, 1) })
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "rw-salary", ObjectType: empType, Property: "salary",
		Read: al, Write: al,
	})

	const readers = 16
	const writers = 8
	const writesPer = 50

	var readerWG sync.WaitGroup
	var writerWG sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < readers; i++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				view, err := h.a.Read("u", empType, "c1")
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				salary := view.Fields["salary"].Value.Int
				if salary < 0 || salary > int64(writers) {
					t.Errorf("impossible serial state salary=%d", salary)
					return
				}
			}
		}()
	}

	for i := 0; i < writers; i++ {
		writerWG.Add(1)
		go func(g int) {
			defer writerWG.Done()
			for j := 0; j < writesPer; j++ {
				if _, err := h.a.Write("u", empType, "c1",
					map[string]Value{"salary": {Int: int64(g + 1)}}); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(i)
	}

	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	if got := h.store.Version(empType, "c1"); got != int64(1+writers*writesPer) {
		t.Fatalf("final version = %d, want %d", got, 1+writers*writesPer)
	}
	if ts := h.store.LastWriteNanos(empType, "c1"); ts != int64(writers*writesPer) {
		t.Fatalf("final timestamp = %d, want %d", ts, writers*writesPer)
	}
	raw, _ := h.store.GetRaw(empType, "c1")
	if salary := raw.Values["salary"].Int; salary < 1 || salary > int64(writers) {
		t.Fatalf("final salary %d must be one writer's serial delta", salary)
	}
}

// TestRejectedWritesHaveNoObservableEffect mixes denied and accepted writes
// concurrently; denied writes must change neither value, version nor
// timestamp, and repeated reads at the same state must be identical.
func TestRejectedWritesHaveNoObservableEffect(t *testing.T) {
	al, _ := allowDeny()
	cfg := Config{
		RowMode: DenyOverrides, PropertyMode: DenyOverrides,
		DefaultRow: EffectDeny, DefaultRead: EffectDeny, DefaultWrite: EffectDeny,
		WriteMode: WriteReject,
	}
	h := newHarness(t, cfg)
	h.store.Put(Instance{
		Type:    empType,
		ID:      "c2",
		Values:  map[string]Value{"name": {Str: "keep"}},
		Present: map[string]bool{"name": true},
	})
	var clockNanos int64
	h.store.SetClock(func() int64 { return atomic.AddInt64(&clockNanos, 1) })
	mustRegRow(t, h.catalog, RowPolicy{
		ID: "row", ObjectType: empType, Subjects: []string{"good"},
		Effect:    EffectAllow,
		Predicate: Predicate{Atoms: []Atom{{Property: "name", Op: OpEq, Str: "keep"}}},
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "p-name", ObjectType: empType, Subjects: []string{"good"},
		Property: "name", Read: al, Write: al,
	})

	const pairs = 32
	var wg sync.WaitGroup
	for i := 0; i < pairs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, err := h.a.Write("bad", empType, "c2", map[string]Value{"name": {Str: "EVIL"}})
				if err == nil || err.Kind != ErrRowInvisible {
					t.Errorf("denied write: %v", err)
				}
				return
			}
			if _, err := h.a.Write("good", empType, "c2",
				map[string]Value{"name": {Str: "keep"}}); err != nil {
				t.Errorf("accepted write: %v", err)
			}
		}(i)
	}
	wg.Wait()

	raw, _ := h.store.GetRaw(empType, "c2")
	if raw.Values["name"].Str != "keep" {
		t.Fatalf("denied writes mutated state: %q", raw.Values["name"].Str)
	}
	if v := h.store.Version(empType, "c2"); v != int64(1+pairs/2) {
		t.Fatalf("version = %d, want %d (only accepted writes bump it)", v, 1+pairs/2)
	}
	if ts := h.store.LastWriteNanos(empType, "c2"); ts != int64(pairs/2) {
		t.Fatalf("timestamp = %d, want %d", ts, pairs/2)
	}
	first, err := h.a.Read("good", empType, "c2")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		next, err := h.a.Read("good", empType, "c2")
		if err != nil {
			t.Fatal(err)
		}
		if !sameView(first, next) {
			t.Fatal("two reads of the same instance at one time must be identical")
		}
	}
}
