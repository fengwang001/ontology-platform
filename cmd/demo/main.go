// Command demo runs the snapshot exporter acceptance checks.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ontology/export"
	"ontology/manifest"
	"ontology/snapshot"
	"ontology/store"
)

var passed, failed int

func check(name string, err error) {
	if err != nil {
		failed++
		fmt.Printf("FAIL %s: %v\n", name, err)
		return
	}
	passed++
	fmt.Printf("OK   %s\n", name)
}

func checkRelease() error {
	st := store.New()
	st.Put("a", []byte("v0"))
	s1 := snapshot.Open(st)
	st.Put("a", []byte("v1"))
	s2 := snapshot.Open(st)
	st.Put("a", []byte("v2"))
	s1.Close()
	if st.RetainedCount() == 0 {
		return errors.New("retained values released while second snapshot still open")
	}
	s2.Close()
	if st.RetainedCount() != 0 {
		return fmt.Errorf("retained count = %d after both snapshots closed", st.RetainedCount())
	}
	return nil
}

func checkRetainedBound() error {
	st := store.New()
	for i := 0; i < 1000; i++ {
		st.Put(fmt.Sprintf("k%04d", i), []byte("v"))
	}
	snap := snapshot.Open(st)
	defer snap.Close()
	for i := 0; i < 50; i++ {
		st.Put(fmt.Sprintf("k%04d", i*7), []byte("new"))
	}
	if n := st.RetainedCount(); n > 50 {
		return fmt.Errorf("retained %d values for 50 modified keys", n)
	}
	return nil
}

func fillStore(n int) *store.Store {
	st := store.New()
	for i := 0; i < n; i++ {
		st.Put(fmt.Sprintf("k%04d", i), []byte(fmt.Sprintf("v%04d", i)))
	}
	return st
}

func dirBytes(dir string) ([]byte, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

func exportTo(st *store.Store, dir string, hook func(*export.Options)) ([]byte, error) {
	snap := snapshot.Open(st)
	defer snap.Close()
	opts := export.Options{Dir: dir, ChunkSize: 256}
	if hook != nil {
		hook(&opts)
	}
	ex, err := export.New(snap, opts)
	if err != nil {
		return nil, err
	}
	if _, err := ex.Run(); err != nil {
		return nil, err
	}
	return dirBytes(dir)
}

func checkCOW() error {
	st := fillStore(100)
	ref, err := exportTo(st, os.TempDir()+"/demo-cow-ref", nil)
	if err != nil {
		return err
	}
	got, err := exportTo(st, os.TempDir()+"/demo-cow", func(o *export.Options) {
		o.OnKey = func(n int) {
			if n == 50 {
				st.Put("k0080", []byte("dirty"))
			}
		}
	})
	if err != nil {
		return err
	}
	if !bytes.Equal(ref, got) {
		return errors.New("export polluted by concurrent write")
	}
	return nil
}

func checkOrder() error {
	st := fillStore(100)
	snap := snapshot.Open(st)
	defer snap.Close()
	crcs := map[bool]uint32{}
	for _, rev := range []bool{false, true} {
		dir := fmt.Sprintf("%s/demo-ord-%v", os.TempDir(), rev)
		ex, _ := export.New(snap, export.Options{Dir: dir, ChunkSize: 256, Reverse: rev})
		if _, err := ex.Run(); err != nil {
			return err
		}
		m, err := manifest.Load(dir)
		if err != nil {
			return err
		}
		crcs[rev] = m.TotalCRC
	}
	if crcs[false] != crcs[true] {
		return fmt.Errorf("total crc differs: %x vs %x", crcs[false], crcs[true])
	}
	return nil
}

func checkReadCount() error {
	st := fillStore(300)
	st.ResetReadCount()
	if _, err := exportTo(st, os.TempDir()+"/demo-rc", nil); err != nil {
		return err
	}
	if n := st.ReadCount(); n != 300 {
		return fmt.Errorf("reads = %d, want 300", n)
	}
	return nil
}

func checkResume() error {
	st := fillStore(100)
	ref, err := exportTo(st, os.TempDir()+"/demo-rsm-ref", nil)
	if err != nil {
		return err
	}
	m, _ := manifest.Load(os.TempDir() + "/demo-rsm-ref")
	boom := errors.New("boom")
	for k := 1; k <= len(m.Chunks); k++ {
		dir := fmt.Sprintf("%s/demo-rsm-%d", os.TempDir(), k)
		os.RemoveAll(dir)
		os.MkdirAll(dir, 0o755)
		snap := snapshot.Open(st)
		done := 0
		ex, _ := export.New(snap, export.Options{Dir: dir, ChunkSize: 256,
			OnChunk: func(int) error {
				done++
				if done >= k {
					return boom
				}
				return nil
			}})
		if _, err := ex.Run(); !errors.Is(err, boom) {
			return fmt.Errorf("k=%d: inject failed: %v", k, err)
		}
		ex2, _ := export.New(snap, export.Options{Dir: dir, ChunkSize: 256})
		if _, err := ex2.Run(); err != nil {
			return fmt.Errorf("k=%d resume: %w", k, err)
		}
		snap.Close()
		got, err := dirBytes(dir)
		if err != nil || !bytes.Equal(ref, got) {
			return fmt.Errorf("k=%d: resumed export differs", k)
		}
	}
	return nil
}

func main() {
	check("retained values released only after both snapshots close", checkRelease())
	check("retained count <= modified keys", checkRetainedBound())
	check("key overwritten during export keeps snapshot value", checkCOW())
	check("export order does not change total checksum", checkOrder())
	check("read count equals key count", checkReadCount())
	check("resume from every chunk k is byte-identical", checkResume())
	fmt.Printf("SUMMARY %d/%d checks passed\n", passed, passed+failed)
}
