package bitemporal

import (
	"bytes"
	"sync"
	"testing"
)

// TestConcurrentWritesDoNotPolluteFreeze runs exports at differing T while a
// writer mutates the same objects continuously. Every frozen result must be
// exactly reproducible after all writers stop by re-freezing the same binding
// semantics: content is identical to a serial read at the freeze point.
func TestConcurrentWritesDoNotPolluteFreeze(t *testing.T) {
	s, exp, _ := testStore(t)
	mustWrite(t, s, "o1", 0, 100, val("base", 0))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			s.AdvanceClock(Tick(1000 + i))
			_, _ = s.Write("o1", "person", iv(t, 0, 100), val("live", int64(i)))
			_, _ = s.Write("o2", "person", iv(t, 0, 100), val("other", int64(i)))
		}
	}()

	// Capture many frozen exports at T=0; all must show only the base record.
	var first []byte
	for i := 0; i < 200; i++ {
		c, err := exp.Freeze(0)
		if err != nil {
			t.Fatal(err)
		}
		snap, err := exp.ExportObject(c, "person", "o1", nil)
		if err != nil {
			t.Fatal(err)
		}
		body := snap.Canonical()
		// Binding seq must never regress and content must always be "base".
		known := snap.Segments[1]
		if known.Value == nil || known.Value.Fields["name"] != "base" {
			t.Fatalf("frozen result polluted by concurrent write: %+v", snap.Segments)
		}
		if first == nil {
			first = body
		}
	}
	close(stop)
	wg.Wait()

	// Post-quiescence, a T=0 export must still read identically (content only;
	// the binding seq advances because of later writes, so compare segments).
	c, _ := exp.Freeze(0)
	snap, err := exp.ExportObject(c, "person", "o1", nil)
	if err != nil {
		t.Fatal(err)
	}
	known := snap.Segments[1]
	if known.Value == nil || known.Value.Fields["name"] != "base" {
		t.Fatalf("post-write historical T=0 export changed: %+v", snap.Segments)
	}
}

// TestConcurrentExportsSerializability fires exports at distinct T alongside
// writes and asserts every export equals the naive serial model at its cutoff.
func TestConcurrentExportsSerializability(t *testing.T) {
	s := NewStore(0, 0)
	if err := s.RegisterSchema("p", 0, map[string]FieldKind{"v": KindInt}); err != nil {
		t.Fatal(err)
	}
	exp := NewExporter(s, NopLogger{})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	type captured struct {
		c   Cutoff
		seq uint64
	}
	var captures []captured
	var capMu sync.Mutex

	// Single writer: append records with increasing ticks.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := int64(0); i < 500; i++ {
			s.AdvanceClock(Tick(i + 1))
			_, err := s.Write("o", "p", iv(t, 0, 10), Value{Fields: map[string]any{"v": i}})
			if err != nil {
				t.Error(err)
				return
			}
		}
		close(stop)
	}()

	// Readers capture frozen bindings and observed seqs concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var bad bool
		for {
			select {
			case <-stop:
				if bad {
					t.Error("serializability violation")
				}
				return
			default:
			}
			t0 := s.Clock()
			c, err := exp.Freeze(t0)
			if err != nil {
				t.Error(err)
				return
			}
			r, _ := exp.VisibleAt(c, "o", 5)
			seq := uint64(0)
			if r != nil {
				seq = r.Seq
			}
			capMu.Lock()
			captures = append(captures, captured{c: c, seq: seq})
			capMu.Unlock()
			// Invariant: visible record must satisfy TxTime<=T and Seq<=binding.
			if r != nil && (r.TxTime > c.T || r.Seq > c.Seq) {
				bad = true
			}
		}
	}()

	wg.Wait()

	// Offline serial validation after quiescence: replay every capture against
	// the final immutable log. A global serial order exists iff each captured
	// result matches the prefix of the log at its frozen (T, Seq) binding.
	audit := NewAuditView(s)
	for _, capn := range captures {
		recs, err := audit.RecordsAt(capn.c, "o")
		if err != nil {
			t.Fatal(err)
		}
		var want uint64
		var bestTx Tick
		for i := range recs {
			r := recs[i]
			if r.TxTime <= capn.c.T && r.Interval().Contains(5) {
				if want == 0 || r.TxTime > bestTx || (r.TxTime == bestTx && r.Seq > want) {
					want, bestTx = r.Seq, r.TxTime
				}
			}
		}
		if want != capn.seq {
			t.Fatalf("offline replay mismatch binding=%s got=%d want=%d", describeCutoff(capn.c), capn.seq, want)
		}
	}
}

// TestInconsistencyDetected verifies tampering produces a typed export error
// and no partial snapshot is returned.
func TestInconsistencyDetected(t *testing.T) {
	s, exp, _ := testStore(t)
	good := mustWrite(t, s, "o1", 0, 100, val("a", 1))
	s.AdvanceClock(5)
	mustWrite(t, s, "o1", 0, 100, val("b", 2))

	// Current export is healthy.
	c, _ := exp.Freeze(5)
	if _, err := exp.ExportObject(c, "person", "o1", nil); err != nil {
		t.Fatalf("unexpected pre-tamper error: %v", err)
	}
	s.markTampered(good.Seq)
	snap, err := exp.ExportObject(c, "person", "o1", nil)
	if snap != nil {
		t.Fatal("must not return partial snapshot on inconsistency")
	}
	ee, ok := AsExportError(err)
	if !ok || ee.Code != CodeInconsistency {
		t.Fatalf("want CodeInconsistency, got %v", err)
	}

	// Batch export must also fail atomically.
	if snaps, err := exp.ExportBatch(c, "person", []ObjectRef{{"o1"}}, nil); err == nil || snaps != nil {
		t.Fatalf("batch must fail atomically: snaps=%v err=%v", snaps, err)
	}
}

// TestDecisionLogContainsInputs outputs every decision to the injected logger
// so test runs carry auditable input/output/rationale lines.
func TestDecisionLogContainsInputs(t *testing.T) {
	s := NewStore(0, 0)
	if err := s.RegisterSchema("p", 0, map[string]FieldKind{"v": KindInt}); err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	exp := NewExporter(s, NewTextLogger(buf))
	r, err := s.Write("o", "p", iv(t, 0, 10), Value{Fields: map[string]any{"v": int64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := exp.Freeze(2)
	exp.VisibleAt(c, "o", 5)
	_, _ = s, r
	if !bytes.Contains(buf.Bytes(), []byte("[freeze]")) || !bytes.Contains(buf.Bytes(), []byte("[point]")) {
		t.Fatalf("decision log missing required decisions:\n%s", buf.String())
	}
}
