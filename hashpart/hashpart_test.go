package hashpart

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPartition(t *testing.T) {
	cases := []struct {
		key string
		n   int
	}{
		{"", 1}, {"", 7}, {"a", 8}, {"abc", 16}, {"键", 3}, {"long-key-xyz", 64},
	}
	for _, c := range cases {
		p := Partition(c.key, c.n)
		if p < 0 || p >= c.n {
			t.Errorf("Partition(%q,%d)=%d out of range", c.key, c.n, p)
		}
		if again := Partition(c.key, c.n); again != p {
			t.Errorf("Partition(%q,%d) unstable: %d != %d", c.key, c.n, again, p)
		}
	}
	// Distribution sanity: 10000 keys over 8 partitions hit every partition.
	seen := map[int]bool{}
	for i := 0; i < 10000; i++ {
		seen[Partition(fmt.Sprintf("k%d", i), 8)] = true
	}
	if len(seen) != 8 {
		t.Errorf("expected all 8 partitions hit, got %d", len(seen))
	}
}

func TestSegmentsAndTempCleanup(t *testing.T) {
	dir := t.TempDir()
	// Leftover temp files from a crashed run must be cleaned, never read.
	for _, name := range []string{"part-002-000000.tmp", "part-002-000001.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("half"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := CountTemps(dir); got != 2 {
		t.Fatalf("CountTemps=%d want 2", got)
	}
	removed, err := CleanTemps(dir)
	if err != nil || removed != 2 {
		t.Fatalf("CleanTemps=%d,%v want 2,nil", removed, err)
	}
	if got := CountTemps(dir); got != 0 {
		t.Fatalf("CountTemps after clean=%d want 0", got)
	}
	// Write two segments for partition 2 and verify listing order.
	for seq := 0; seq < 2; seq++ {
		w, err := NewWriter(dir, 2, seq)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Write([]byte(fmt.Sprintf("seg-%d", seq))); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	segs, err := ListSegments(dir, 2)
	if err != nil || len(segs) != 2 {
		t.Fatalf("ListSegments=%v,%v", segs, err)
	}
	if filepath.Base(segs[0]) != "part-002-000000.spill" {
		t.Errorf("unexpected first segment %s", segs[0])
	}
	other, err := ListSegments(dir, 3)
	if err != nil || len(other) != 0 {
		t.Errorf("partition 3 should have no segments: %v", other)
	}
}
