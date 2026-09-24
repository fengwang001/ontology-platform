package progress

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ontology/batch"
)

func buildFile(t *testing.T, dir string, n, chunks int) (*File, []byte) {
	t.Helper()
	path := filepath.Join(dir, "b.progress")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	f, class, err := Open(path, "b")
	if err != nil || class != ClassOK {
		t.Fatalf("Open: class=%v err=%v", class, err)
	}
	for i := 0; i < chunks; i++ {
		if err := f.Flush(batch.Interval{Start: i * n, End: (i + 1) * n}); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return f, data
}

func TestProgress(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file means fresh start", func(t *testing.T) {
		f, class, err := Open(filepath.Join(dir, "none.progress"), "x")
		if err != nil || class != ClassOK || f.Contiguous() != 0 {
			t.Fatalf("class=%v err=%v contiguous=%d", class, err, f.Contiguous())
		}
	})

	t.Run("roundtrip", func(t *testing.T) {
		_, data := buildFile(t, dir, 2, 5)
		f, class := Recover(data)
		if class != ClassOK || f.BatchID != "b" || f.Contiguous() != 10 || len(f.Intervals) != 5 {
			t.Fatalf("class=%v id=%q contiguous=%d intervals=%d",
				class, f.BatchID, f.Contiguous(), len(f.Intervals))
		}
	})

	t.Run("truncate every byte", func(t *testing.T) {
		orig, data := buildFile(t, dir, 2, 5)
		tally := map[Class]int{}
		for cut := 1; cut < len(data); cut++ {
			f, class := Recover(data[:cut])
			tally[class]++
			if err := class.Err(); err != nil {
				switch class {
				case ClassHeaderIncomplete:
					if !errors.Is(err, ErrHeaderIncomplete) {
						t.Fatalf("cut=%d wrong sentinel %v", cut, err)
					}
				case ClassIntervalIncomplete:
					if !errors.Is(err, ErrIntervalIncomplete) {
						t.Fatalf("cut=%d wrong sentinel %v", cut, err)
					}
				case ClassCRCMismatch:
					if !errors.Is(err, ErrCRCMismatch) {
						t.Fatalf("cut=%d wrong sentinel %v", cut, err)
					}
				default:
					t.Fatalf("cut=%d unexpected class %v with err", cut, class)
				}
			}
			if len(f.Intervals) > len(orig.Intervals) {
				t.Fatalf("cut=%d recovered more intervals than original", cut)
			}
			for i, iv := range f.Intervals {
				if iv != orig.Intervals[i] {
					t.Fatalf("cut=%d interval %d = %v, want prefix %v", cut, i, iv, orig.Intervals[i])
				}
			}
			if f.Contiguous() > orig.Contiguous() {
				t.Fatalf("cut=%d contiguous %d > original %d", cut, f.Contiguous(), orig.Contiguous())
			}
		}
		for _, c := range []Class{ClassHeaderIncomplete, ClassIntervalIncomplete, ClassCRCMismatch} {
			if tally[c] == 0 {
				t.Fatalf("class %v never observed; tally=%v", c, tally)
			}
		}
		t.Logf("tally: ok=%d header=%d interval=%d crc=%d total=%d",
			tally[ClassOK], tally[ClassHeaderIncomplete], tally[ClassIntervalIncomplete],
			tally[ClassCRCMismatch], len(data)-1)
	})

	t.Run("open rewrites truncated file to recovered prefix", func(t *testing.T) {
		_, data := buildFile(t, dir, 2, 5)
		path := filepath.Join(dir, "b.progress")
		cut := strings.LastIndexByte(string(data), '\n') - 3 // 落在末行 CRC 内
		if err := os.WriteFile(path, data[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		f, class, err := Open(path, "b")
		if err != nil || class != ClassCRCMismatch || f.Contiguous() != 8 {
			t.Fatalf("class=%v err=%v contiguous=%d", class, err, f.Contiguous())
		}
		after, _ := os.ReadFile(path)
		if _, c := Recover(after); c != ClassOK {
			t.Fatalf("rewritten file not clean: class=%v", c)
		}
	})
}
