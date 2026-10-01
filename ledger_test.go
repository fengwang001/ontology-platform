package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestNewLedger(t *testing.T) {
	if _, err := NewLedger(1, 0, 1, 0); err != nil {
		t.Fatalf("NewLedger() error = %v", err)
	}

	cases := []struct {
		name           string
		total, s, e, w int64
	}{
		{"total zero", 0, 0, 1, 0},
		{"negative reserve", 10, -1, 1, 0},
		{"reserve above total", 10, 11, 1, 0},
		{"zero extent", 10, 0, 0, 0},
		{"negative watermark", 10, 0, 1, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLedger(tc.total, tc.s, tc.e, tc.w); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestWriteReservationLimits(t *testing.T) {
	t.Run("non-privileged exact limit succeeds", func(t *testing.T) {
		l, _ := NewLedger(6, 2, 4, 10)
		flushed, err := l.Write(1, 3, false)
		if err != nil || len(flushed) != 0 {
			t.Fatalf("Write() = %v, %v; want empty flush, nil", flushed, err)
		}
		if got := l.Reserved(); got != 4 {
			t.Fatalf("Reserved() = %d, want 4", got)
		}
	})

	t.Run("non-privileged one over rejected atomically", func(t *testing.T) {
		l, _ := NewLedger(5, 2, 4, 10)
		_, err := l.Write(1, 3, false)
		if !errors.Is(err, ErrNoSpace) {
			t.Fatalf("Write() error = %v, want ErrNoSpace", err)
		}
		assertNoFile(t, l, 1)
		assertSnapshot(t, l, 5, 0, 3, 5)
	})

	t.Run("privileged exact limit ignores special reserve", func(t *testing.T) {
		l, _ := NewLedger(4, 2, 4, 10)
		if _, err := l.Write(1, 3, true); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	})

	t.Run("privileged one over rejected atomically", func(t *testing.T) {
		l, _ := NewLedger(3, 2, 4, 10)
		_, err := l.Write(1, 3, true)
		if !errors.Is(err, ErrNoSpace) {
			t.Fatalf("Write() error = %v, want ErrNoSpace", err)
		}
		assertNoFile(t, l, 1)
		assertSnapshot(t, l, 3, 0, 1, 3)
	})
}

func TestIndexBlockBoundary(t *testing.T) {
	l, _ := NewLedger(100, 0, 4, 100)
	if _, err := l.Write(1, 3, false); err != nil {
		t.Fatal(err)
	}
	if err := l.Flush(1); err != nil {
		t.Fatal(err)
	}

	before := l.Reserved()
	if _, err := l.Write(1, 1, false); err != nil {
		t.Fatal(err)
	}
	if got := l.Reserved() - before; got != 1 {
		t.Fatalf("reservation from 3 to 4 = %d, want 1 (no new index)", got)
	}
	if err := l.Flush(1); err != nil {
		t.Fatal(err)
	}

	before = l.Reserved()
	if _, err := l.Write(1, 1, false); err != nil {
		t.Fatal(err)
	}
	if got := l.Reserved() - before; got != 2 {
		t.Fatalf("reservation from 4 to 5 = %d, want 2 (new index)", got)
	}
}

func TestFlushPreservesAvailableSpace(t *testing.T) {
	l, _ := NewLedger(100, 10, 4, 100)
	if _, err := l.Write(1, 3, false); err != nil {
		t.Fatal(err)
	}
	before := snapshot{l.Free(), l.Reserved(), l.Avail(false), l.Avail(true)}
	if err := l.Flush(1); err != nil {
		t.Fatal(err)
	}
	after := snapshot{l.Free(), l.Reserved(), l.Avail(false), l.Avail(true)}

	if before.user != after.user || before.priv != after.priv {
		t.Fatalf("Avail changed: before %+v after %+v", before, after)
	}
	if after.free != 96 || after.reserved != 0 || after.user != 86 || after.priv != 96 {
		t.Fatalf("after flush = %+v, want free 96 reserved 0 user 86 priv 96", after)
	}
}

func TestTruncateOrderAndIndexRelease(t *testing.T) {
	l, _ := NewLedger(100, 0, 4, 100)
	if _, err := l.Write(1, 5, false); err != nil {
		t.Fatal(err)
	}
	if err := l.Flush(1); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write(1, 3, false); err != nil {
		t.Fatal(err)
	}

	if err := l.Truncate(1, 2); err != nil {
		t.Fatal(err)
	}
	if got := l.Dirty(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("Dirty() after partial delayed truncate = %v, want [1]", got)
	}

	if err := l.Truncate(1, 2); err != nil {
		t.Fatal(err)
	}
	if got := l.Free(); got != 95 {
		t.Fatalf("Free() = %d, want 95", got)
	}
	if got := l.Dirty(); len(got) != 0 {
		t.Fatalf("Dirty() = %v, want empty", got)
	}

	if got := l.Free(); got != 95 {
		t.Fatalf("Free() after delayed removal = %d, want 95", got)
	}
	if err := l.Truncate(1, 1); err != nil {
		t.Fatal(err)
	}
	if got := l.Free(); got != 96 {
		t.Fatalf("Free() after crossing E boundary = %d, want 96", got)
	}
	if err := l.Truncate(1, 5); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("Truncate() error = %v, want ErrFileTooLarge", err)
	}
}

func TestWatermarkAndDirtyGenerations(t *testing.T) {
	t.Run("exact watermark stays and one over flushes oldest", func(t *testing.T) {
		l, _ := NewLedger(100, 0, 4, 5)
		assertWrite(t, l, 1, 3, false)
		assertWrite(t, l, 2, 2, false)
		if got := l.Dirty(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("Dirty() = %v, want [1 2]", got)
		}
		flushed, err := l.Write(3, 1, false)
		if err != nil || len(flushed) != 1 || flushed[0] != 1 {
			t.Fatalf("Write() = %v, %v; want [1], nil", flushed, err)
		}
	})

	t.Run("append keeps generation and rewrite after flush gets new one", func(t *testing.T) {
		l, _ := NewLedger(100, 0, 4, 20)
		assertWrite(t, l, 1, 1, false)
		assertWrite(t, l, 2, 1, false)
		assertWrite(t, l, 1, 1, false)
		if err := l.Flush(1); err != nil {
			t.Fatal(err)
		}
		assertWrite(t, l, 3, 1, false)
		assertWrite(t, l, 1, 1, false)
		if got := l.Dirty(); len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 1 {
			t.Fatalf("Dirty() = %v, want [2 3 1]", got)
		}
	})

	t.Run("writeback can flush just-written file", func(t *testing.T) {
		l, _ := NewLedger(100, 0, 4, 5)
		assertWrite(t, l, 1, 3, false)
		assertWrite(t, l, 2, 1, false)
		flushed, err := l.Write(1, 3, false)
		if err != nil || len(flushed) != 1 || flushed[0] != 1 {
			t.Fatalf("Write() = %v, %v; want [1], nil", flushed, err)
		}
		if got := l.Dirty(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("Dirty() = %v, want [2]", got)
		}
	})

	t.Run("zero watermark flushes every write", func(t *testing.T) {
		l, _ := NewLedger(100, 0, 4, 0)
		flushed, err := l.Write(1, 3, false)
		if err != nil || len(flushed) != 1 || flushed[0] != 1 {
			t.Fatalf("first Write() = %v, %v; want [1]", flushed, err)
		}
		flushed, err = l.Write(2, 2, false)
		if err != nil || len(flushed) != 1 || flushed[0] != 2 {
			t.Fatalf("second Write() = %v, %v; want [2]", flushed, err)
		}
		if got := l.Dirty(); len(got) != 0 {
			t.Fatalf("Dirty() = %v, want empty", got)
		}
	})
}

func TestRejectionOrderAndRecreate(t *testing.T) {
	l, _ := NewLedger(100, 0, 4, 0)
	if _, err := l.Write(1, 1, false); err != nil {
		t.Fatal(err)
	}

	if _, err := l.Write(-1, 0, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Write error = %v, want ErrInvalidArgument", err)
	}
	if err := l.Flush(9); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("missing Flush error = %v, want ErrFileNotFound", err)
	}
	if err := l.Flush(1); !errors.Is(err, ErrNoDelayedBlocks) {
		t.Fatalf("clean Flush error = %v, want ErrNoDelayedBlocks", err)
	}
	if err := l.Truncate(9, 1); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("missing Truncate error = %v, want ErrFileNotFound", err)
	}
	if err := l.Truncate(1, 2); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("large Truncate error = %v, want ErrFileTooLarge", err)
	}

	l.mu.Lock()
	counterAfterSuccess := l.counter
	l.mu.Unlock()
	_, err := l.Write(2, 200, false)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("large Write error = %v, want ErrNoSpace", err)
	}
	assertNoFile(t, l, 2)
	l.mu.Lock()
	counterAfterRejection := l.counter
	l.mu.Unlock()
	if counterAfterRejection != counterAfterSuccess {
		t.Fatalf("counter changed %d to %d after rejected write", counterAfterSuccess, counterAfterRejection)
	}

	if err := l.Unlink(1); err != nil {
		t.Fatal(err)
	}
	if err := l.Unlink(1); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("second Unlink error = %v, want ErrFileNotFound", err)
	}
	if _, err := l.Write(1, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := l.Flush(1); !errors.Is(err, ErrNoDelayedBlocks) {
		t.Fatalf("Flush after recreate error = %v, want ErrNoDelayedBlocks for existing file", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	l, _ := NewLedger(300, 0, 4, 0)
	stop := make(chan struct{})
	var writers, readers sync.WaitGroup

	for i := int64(0); i < 4; i++ {
		writers.Add(1)
		go func(id int64) {
			defer writers.Done()
			for j := 0; j < 50; j++ {
				if _, err := l.Write(id, 1, false); err != nil {
					t.Errorf("Write(%d): %v", id, err)
					return
				}
			}
		}(i)
	}

	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					free := l.Free()
					reserved := l.Reserved()
					if free < 0 || reserved > free {
						t.Errorf("invariant broken: F=%d R=%d", free, reserved)
						return
					}
					_ = l.Avail(false)
					_ = l.Avail(true)
					_ = l.Dirty()
				}
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()
}

type snapshot struct {
	free, reserved, user, priv int64
}

func assertWrite(t *testing.T, l *Ledger, id, n int64, privileged bool) {
	t.Helper()
	if _, err := l.Write(id, n, privileged); err != nil {
		t.Fatalf("Write(%d, %d) error = %v", id, n, err)
	}
}

func assertNoFile(t *testing.T, l *Ledger, id int64) {
	t.Helper()
	if err := l.Unlink(id); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("rejected write created file %d; Unlink error = %v", id, err)
	}
}

func assertSnapshot(t *testing.T, l *Ledger, free, reserved, user, priv int64) {
	t.Helper()
	got := snapshot{l.Free(), l.Reserved(), l.Avail(false), l.Avail(true)}
	want := snapshot{free, reserved, user, priv}
	if got != want {
		t.Fatalf("snapshot = %+v, want %+v", got, want)
	}
}
