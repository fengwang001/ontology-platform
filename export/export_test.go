package export_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ontology/export"
	"ontology/manifest"
	"ontology/snapshot"
	"ontology/store"
)

func fillStore(n int) *store.Store {
	st := store.New()
	for i := 0; i < n; i++ {
		st.Put(fmt.Sprintf("k%05d", i), []byte(fmt.Sprintf("v%05d", i)))
	}
	return st
}

func dirBytes(t *testing.T, dir string) []byte {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b...)
	}
	return out
}

func exportOnce(t *testing.T, snap *snapshot.Snapshot, dir string, size int) *manifest.Manifest {
	t.Helper()
	ex, err := export.New(snap, export.Options{Dir: dir, ChunkSize: size})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ex.Run()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestShapes(t *testing.T) {
	cases := []struct {
		name      string
		keys      []string
		chunkSize int
		wantChunk int
		wantErr   error
	}{
		{"empty store", nil, 128, 0, nil},
		{"single key", []string{"a"}, 128, 1, nil},
		{"chunk bigger than data", []string{"a", "b", "c"}, 1 << 20, 1, nil},
		{"zero chunk size", []string{"a"}, 0, 0, export.ErrChunkSizeZero},
		{"negative chunk size", []string{"a"}, -5, 0, export.ErrChunkSizeZero},
		{"empty key and empty value", []string{"", "k"}, 128, 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			for i, k := range tc.keys {
				var v []byte
				if i == 0 {
					v = []byte("x")
				}
				st.Put(k, v)
			}
			snap := snapshot.Open(st)
			defer snap.Close()
			dir := t.TempDir()
			ex, err := export.New(snap, export.Options{Dir: dir, ChunkSize: tc.chunkSize})
			if errors.Is(err, tc.wantErr) && tc.wantErr != nil {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			m, err := ex.Run()
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Chunks) != tc.wantChunk || m.TotalKeys != len(tc.keys) || !m.Complete {
				t.Fatalf("got chunks=%d keys=%d complete=%v", len(m.Chunks), m.TotalKeys, m.Complete)
			}
		})
	}
}

func TestOrderIndependent(t *testing.T) {
	st := fillStore(300)
	snap := snapshot.Open(st)
	defer snap.Close()
	var crc [2]uint32
	for i, rev := range []bool{false, true} {
		ex, _ := export.New(snap, export.Options{Dir: t.TempDir(), ChunkSize: 256, Reverse: rev})
		m, err := ex.Run()
		if err != nil {
			t.Fatal(err)
		}
		crc[i] = m.TotalCRC
	}
	if crc[0] != crc[1] {
		t.Fatalf("total crc differs by order: %x vs %x", crc[0], crc[1])
	}
}

func TestReadCountAndPeak(t *testing.T) {
	st := fillStore(1000)
	snap := snapshot.Open(st)
	defer snap.Close()
	st.ResetReadCount()
	ex, _ := export.New(snap, export.Options{Dir: t.TempDir(), ChunkSize: 4096})
	if _, err := ex.Run(); err != nil {
		t.Fatal(err)
	}
	if n := st.ReadCount(); n != 1000 {
		t.Fatalf("reads = %d, want 1000", n)
	}
	if p := ex.Stats().PeakChunkBytes; p > 4096 {
		t.Fatalf("peak chunk buffer = %d > 4096", p)
	}
}

func TestCOWDuringExport(t *testing.T) {
	st := fillStore(100)
	s1 := snapshot.Open(st)
	defer s1.Close()
	ref := exportOnce(t, s1, t.TempDir(), 256)
	s2 := snapshot.Open(st)
	defer s2.Close()
	dir := t.TempDir()
	ex, _ := export.New(s2, export.Options{Dir: dir, ChunkSize: 256,
		OnKey: func(n int) {
			if n == 50 {
				st.Put("k00080", []byte("dirty"))
			}
		}})
	m, err := ex.Run()
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalCRC != ref.TotalCRC {
		t.Fatalf("export polluted: crc %x != %x", m.TotalCRC, ref.TotalCRC)
	}
	if v, _, _ := s2.Get("k00080"); string(v) != "v00080" {
		t.Fatalf("snapshot sees %q", v)
	}
}

func TestResumeEveryK(t *testing.T) {
	st := fillStore(200)
	snap := snapshot.Open(st)
	defer snap.Close()
	refDir := t.TempDir()
	rm := exportOnce(t, snap, refDir, 128)
	ref := dirBytes(t, refDir)
	boom := errors.New("boom")
	for k := 1; k <= len(rm.Chunks); k++ {
		dir := t.TempDir()
		done := 0
		ex, _ := export.New(snap, export.Options{Dir: dir, ChunkSize: 128,
			OnChunk: func(int) error {
				done++
				if done >= k {
					return boom
				}
				return nil
			}})
		if _, err := ex.Run(); !errors.Is(err, boom) {
			t.Fatalf("k=%d: inject got %v", k, err)
		}
		ex2, _ := export.New(snap, export.Options{Dir: dir, ChunkSize: 128})
		if _, err := ex2.Run(); err != nil {
			t.Fatalf("k=%d: resume: %v", k, err)
		}
		if got := dirBytes(t, dir); !bytes.Equal(ref, got) {
			t.Fatalf("k=%d: resumed export differs", k)
		}
	}
}

func TestConcurrentExports(t *testing.T) {
	st := fillStore(2000)
	snap := snapshot.Open(st)
	defer snap.Close()
	ref := dirBytes(t, func() string {
		dir := t.TempDir()
		exportOnce(t, snap, dir, 512)
		return dir
	}())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			st.Put(fmt.Sprintf("k%05d", i), []byte("concurrent"))
		}
	}()
	dirs := make([]string, 4)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			ex, _ := export.New(snap, export.Options{Dir: dir, ChunkSize: 512})
			if _, err := ex.Run(); err != nil {
				t.Error(err)
			}
		}(dirs[i])
	}
	wg.Wait()
	for _, dir := range dirs {
		if got := dirBytes(t, dir); !bytes.Equal(ref, got) {
			t.Fatal("concurrent export differs from solo export")
		}
	}
}
