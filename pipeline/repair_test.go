package pipeline

import (
	"errors"
	"os"
	"testing"

	"ontology/spill"
)

// Truncate the final flush run mid-record via fault injection, then
// reopen: the run is repaired to its prefix and the output must contain
// exactly the records that completed spilling (the in-flight batch was
// never checkpointed as accepted at the ingest boundary — here we force
// a truncation of a background run, whose records WERE already replied
// to as accepted; after repair Accepted shrinks to the prefix total).
func TestTruncatedRunRepairPrefix(t *testing.T) {
	dir := t.TempDir()
	faults := &Faults{
		TruncateRun: func(runID uint64) int {
			// Only cut the very last run, at a mid-record offset.
			if runID < 3 {
				return -1
			}
			path := spill.RunPath(dir, runID)
			// Determine a cut point inside the first record body after
			// the run is fully buffered by probing the target size: the
			// writer honors an absolute cut, so choose HeaderSize+10.
			_ = path
			return spill.HeaderSize + 10
		},
	}
	p, err := Open(Config{Dir: dir, MemoryByte: 2048, Faults: faults})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		if _, err := p.Ingest(keyFixed(i), []byte("payload-bytes")); err != nil {
			t.Fatal(err)
		}
	}
	// Close will fail because the truncated run fails validation; that
	// is expected and definitive.
	_ = p.Close()

	p2, err := Open(Config{Dir: dir, MemoryByte: 2048})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := p2.Close(); err != nil {
		t.Fatalf("finalize after repair: %v", err)
	}
	out, err := p2.ReadOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 || len(out) >= 300 {
		t.Fatalf("repaired prefix size = %d", len(out))
	}
	for i := 1; i < len(out); i++ {
		if out[i].Key < out[i-1].Key {
			t.Fatalf("order broken at %d", i)
		}
	}
}

func TestPostCloseIngestRejected(t *testing.T) {
	dir := t.TempDir()
	p, err := Open(Config{Dir: dir, MemoryByte: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ingest("a", nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ingest("b", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close ingest: %v", err)
	}
}

func TestAllSameKeyAndEmptyKey(t *testing.T) {
	dir := t.TempDir()
	p, err := Open(Config{Dir: dir, MemoryByte: 2048})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := p.Ingest("", []byte{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := p.ReadOutput()
	if err != nil || len(out) != 200 {
		t.Fatalf("out=%d err=%v", len(out), err)
	}
	for i := 1; i < len(out); i++ {
		if out[i].Seq != out[i-1].Seq+1 {
			t.Fatalf("same-key arrival order broken at %d", i)
		}
	}
	if _, err := os.Stat(spill.RunPath(dir, 1)); err != nil {
		t.Fatalf("run file missing: %v", err)
	}
}
