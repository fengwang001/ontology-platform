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
	"ontology/snapshot"
	"ontology/store"
)

func setup(t *testing.T, n int) (*store.Store, string) {
	t.Helper()
	st := store.New()
	for i := 0; i < n; i++ {
		st.Put(fmt.Sprintf("k%04d", i), []byte(fmt.Sprintf("value-%04d", i)))
	}
	return st, t.TempDir()
}

func readData(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, export.DataName))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestResumeEveryK：对每个 k=1..块数 逐块续传，结果与一次性导出逐字节相同。
func TestResumeEveryK(t *testing.T) {
	st, dir := setup(t, 200)
	snap := snapshot.Take(st)
	full, err := export.Run(export.Options{
		Dir: dir, Snapshot: snap, Order: snapshot.Ascending, BlockSize: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := len(full.Manifest().Blocks)
	want := readData(t, dir)
	for k := 1; k <= blocks; k++ {
		d := filepath.Join(t.TempDir(), "d")
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := export.Run(export.Options{
			Dir: d, Snapshot: snap, Order: snapshot.Ascending, BlockSize: 256,
			Stop: func(last int) error {
				if last == k-1 {
					return errInterrupt
				}
				return nil
			},
		})
		if !errors.Is(err, errInterrupt) {
			t.Fatalf("k=%d interrupt err=%v", k, err)
		}
		if _, err := export.Resume(export.Options{
			Dir: d, Snapshot: snap, Order: snapshot.Ascending, BlockSize: 256,
		}, k); err != nil {
			t.Fatalf("k=%d resume: %v", k, err)
		}
		if got := readData(t, d); !bytes.Equal(got, want) {
			t.Fatalf("k=%d resumed file differs (%d vs %d bytes)", k, len(got), len(want))
		}
	}
	snap.Close()
}

var errInterrupt = errors.New("simulated interruption")

// TestOrder：字典序与逆序导出的内容集合相同（总校验和一致），块布局可不同。
func TestOrder(t *testing.T) {
	st, _ := setup(t, 100)
	dirs := []string{t.TempDir(), t.TempDir()}
	orders := []snapshot.Order{snapshot.Ascending, snapshot.Descending}
	var sums [][]byte
	for i, order := range orders {
		snap := snapshot.Take(st)
		e, err := export.Run(export.Options{
			Dir: dirs[i], Snapshot: snap, Order: order, BlockSize: 128,
		})
		if err != nil {
			t.Fatal(err)
		}
		sums = append(sums, e.Manifest().Checksum)
		snap.Close()
	}
	if !bytes.Equal(sums[0], sums[1]) {
		t.Fatal("ascending/descending checksums differ")
	}
}

// TestPeakBlock：单块驻留峰值不超过块大小；覆盖单块、空存储、块大小为 0。
func TestPeakBlock(t *testing.T) {
	cases := []struct {
		name     string
		n, size  int
		wantErr  error
		oneBlock bool
	}{
		{"empty", 0, 128, nil, true},
		{"single", 1, 4096, nil, true},
		{"multi", 300, 204, nil, false},
		{"zero-size", 1, 0, export.ErrBlockSize, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, dir := setup(t, tc.n)
			snap := snapshot.Take(st)
			e, err := export.Run(export.Options{
				Dir: dir, Snapshot: snap, BlockSize: tc.size,
			})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v want %v", err, tc.wantErr)
				}
				snap.Close()
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.PeakBlockBytes() > tc.size && tc.n > 1 {
				t.Fatalf("peak=%d > blockSize=%d", e.PeakBlockBytes(), tc.size)
			}
			if tc.oneBlock && len(e.Manifest().Blocks) != 1 && tc.n > 0 {
				t.Fatalf("blocks=%d want 1", len(e.Manifest().Blocks))
			}
			snap.Close()
		})
	}
}

// TestExpiredResume：快照关闭后续传被可判定拒绝，且已导出部分原样保留。
func TestExpiredResume(t *testing.T) {
	st, dir := setup(t, 100)
	snap := snapshot.Take(st)
	_, err := export.Run(export.Options{
		Dir: dir, Snapshot: snap, BlockSize: 128,
		Stop: func(last int) error {
			if last == 1 {
				return errInterrupt
			}
			return nil
		},
	})
	if !errors.Is(err, errInterrupt) {
		t.Fatal(err)
	}
	partial := readData(t, dir)
	snap.Close()
	_, err = export.Resume(export.Options{
		Dir: dir, Snapshot: snap, BlockSize: 128,
	}, -1)
	if !errors.Is(err, export.ErrSnapshotExpired) {
		t.Fatalf("err=%v want ErrSnapshotExpired", err)
	}
	if got := readData(t, dir); !bytes.Equal(got, partial) {
		t.Fatal("partial export was modified after expired resume")
	}
}

// TestConcurrentExports：4 个并发导出（各自快照）与单独导出逐字节相同。
func TestConcurrentExports(t *testing.T) {
	st, dir := setup(t, 300)
	single := snapshot.Take(st)
	if err := os.MkdirAll(filepath.Join(dir, "ref"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := export.Run(export.Options{
		Dir: filepath.Join(dir, "ref"), Snapshot: single, BlockSize: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	single.Close()
	want := readData(t, filepath.Join(dir, "ref"))

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			snap := snapshot.Take(st)
			defer snap.Close()
			d := filepath.Join(dir, fmt.Sprintf("e%d", id))
			_ = os.MkdirAll(d, 0o700)
			_, gerr := export.Run(export.Options{Dir: d, Snapshot: snap, BlockSize: 200})
			if gerr != nil {
				errs <- gerr
				return
			}
			if got := readData(t, d); !bytes.Equal(got, want) {
				errs <- fmt.Errorf("export %d differs", id)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
