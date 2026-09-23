package res

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestRegistryAccounting(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		run  func(*Registry)
		want Stats
	}{
		{
			"opens and closes balance",
			func(r *Registry) { r.Opened(); r.Opened(); r.Closed(); r.Closed() },
			Stats{Opens: 2, Closes: 2},
		},
		{
			"spill lifecycle removes",
			func(r *Registry) {
				f, err := r.CreateSpill()
				if err != nil {
					t.Fatal(err)
				}
				if err := r.RemoveSpill(f); err != nil {
					t.Fatal(err)
				}
			},
			Stats{SpillCreated: 1, SpillRemoved: 1},
		},
		{
			"double remove is idempotent",
			func(r *Registry) {
				f, _ := r.CreateSpill()
				name := f.Name()
				_ = r.RemoveSpill(f)
				_ = r.RemoveSpill(f)
				if g, err := openProbe(name); err == nil {
					_ = g.Close()
					t.Fatal("spill file still on disk")
				}
			},
			Stats{SpillCreated: 1, SpillRemoved: 1},
		},
		{
			"peak rows only grows",
			func(r *Registry) { r.NoteRows(3); r.NoteRows(9); r.NoteRows(2) },
			Stats{PeakRows: 9},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(dir)
			tc.run(r)
			got := r.Snapshot()
			if got != tc.want {
				t.Fatalf("stats = %+v, want %+v", got, tc.want)
			}
			if !got.Balanced() {
				t.Fatalf("expected balanced: %+v", got)
			}
		})
	}
}

func TestRegistryLeakDetected(t *testing.T) {
	r := NewRegistry(t.TempDir())
	r.Opened()
	f, err := r.CreateSpill()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		tweak    func()
		balanced bool
	}{
		{"unclosed operator", func() {}, false},
		{"leaked spill", func() { _ = r.Closed() }, false},
		{"everything released", func() { _ = r.RemoveSpill(f) }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.tweak()
			if got := r.Snapshot().Balanced(); got != tc.balanced {
				t.Fatalf("Balanced=%v want %v", got, tc.balanced)
			}
		})
	}
}

func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry(t.TempDir())
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				r.Opened()
				f, err := r.CreateSpill()
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := f.WriteString("x"); err != nil {
					t.Error(err)
				}
				r.NoteRows(i)
				_ = r.RemoveSpill(f)
				r.Closed()
			}
		}()
	}
	wg.Wait()
	st := r.Snapshot()
	if !st.Balanced() {
		t.Fatalf("unbalanced after concurrency: %+v", st)
	}
	if st.Opens != 16*50 || st.SpillCreated != 16*50 {
		t.Fatalf("lost updates: %+v", st)
	}
	if matches, _ := filepath.Glob(filepath.Join(r.Dir(), "volcano-spill-*")); len(matches) != 0 {
		t.Fatalf("files left on disk: %v", matches)
	}
}
