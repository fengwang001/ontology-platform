package api

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// model is the naive reference: literal grant conditions per request; releases re-evaluate the FIFO queue ("w"+id / "r"+id entries).
type model struct {
	readers map[string]bool
	writer  string
	queue   []string
	waiting int // queued writers
}

func (m *model) acquire(id string, write bool) bool {
	if !write && m.writer == "" && m.waiting == 0 {
		m.readers[id] = true
		return true
	}
	if write && m.writer == "" && len(m.readers) == 0 && len(m.queue) == 0 {
		m.writer = id
		return true
	}
	if write {
		m.waiting++
		m.queue = append(m.queue, "w"+id)
	} else {
		m.queue = append(m.queue, "r"+id)
	}
	return false
}
func (m *model) settle() {
	if m.writer != "" {
		return
	}
	wi := -1 // first queued writer
	for i, q := range m.queue {
		if q[0] == 'w' {
			wi = i
			break
		}
	}
	if wi >= 0 {
		if len(m.readers) > 0 {
			return
		}
		m.writer = m.queue[wi][1:]
		m.queue = append(m.queue[:wi], m.queue[wi+1:]...)
		m.waiting--
		return
	}
	for _, q := range m.queue { // no writer waiting: all readers enter
		m.readers[q[1:]] = true
	}
	m.queue = nil
}

// checkModel compares one random interleaving against the naive model.
func checkModel(t *testing.T, steps int, seed int64) {
	a, m, r := New(), &model{readers: map[string]bool{}}, rand.New(rand.NewSource(seed))
	for i := 0; i < steps; i++ {
		id, write := []string{"R1", "R2", "R3", "W1", "W2"}[r.Intn(5)], r.Intn(2) == 0
		acq, rel, held := a.AcquireRead, a.ReleaseRead, m.readers[id]
		if write {
			acq, rel, held = a.AcquireWrite, a.ReleaseWrite, m.writer == id
		}
		if r.Intn(2) == 0 {
			got, err := acq(id)
			if want := m.acquire(id, write); err != nil || got != want {
				t.Fatalf("seed %d step %d acquire(%s,%v): got %v,%v want %v", seed, i, id, write, got, err, want)
			}
		} else {
			err := rel(id)
			if write && m.writer == id {
				m.writer = ""
			}
			if !write {
				delete(m.readers, id)
			}
			m.settle()
			if (err == nil) != held {
				t.Fatalf("seed %d step %d release(%s,%v): err=%v held=%v", seed, i, id, write, err, held)
			}
		}
		if got, want := fmt.Sprint(a.Readers(), a.Writer()), fmt.Sprint(slices.Sorted(maps.Keys(m.readers)), m.writer); got != want {
			t.Fatalf("seed %d step %d: state %s, model %s", seed, i, got, want)
		}
	}
}
func TestReferenceModelRandom(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		checkModel(t, 200, seed)
	}
}
func TestEightStepTrace(t *testing.T) {
	if err := checkTrace(); err != nil {
		t.Fatal(err)
	}
}
func TestWriterPreference(t *testing.T) {
	if err := checkWriterPref(); err != nil {
		t.Fatal(err)
	}
}
func TestFaultInjection(t *testing.T) {
	if err := checkFaults(); err != nil {
		t.Fatal(err)
	}
	if ErrEmptyID == ErrNotHeld || ErrNotHeld == ErrDoubleRelease || ErrEmptyID == ErrDoubleRelease {
		t.Fatal("sentinel errors must be mutually distinguishable")
	}
}
func TestConcurrentMutualExclusion(t *testing.T) {
	a := New()
	var wg sync.WaitGroup
	var bad atomic.Int32
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rid, wid := fmt.Sprintf("R%d", g), fmt.Sprintf("W%d", g)
			for i := 0; i < 50; i++ {
				// A held lock pins the state: the other side cannot
				// appear while we hold our lock.
				if ok, _ := a.AcquireRead(rid); ok {
					if a.Writer() != "" {
						bad.Add(1)
					}
					a.ReleaseRead(rid)
				}
				if ok, _ := a.AcquireWrite(wid); ok {
					if len(a.Readers()) > 0 || a.Writer() != wid {
						bad.Add(1)
					}
					a.ReleaseWrite(wid)
				}
			}
		}(g)
	}
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatal("mutual exclusion violated under concurrency")
	}
}
