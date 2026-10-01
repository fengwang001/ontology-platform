package quota

import (
	"errors"
	"testing"
)

func TestSmokeSpecExample(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 100, -1))
	id1, err := l.Mkdir(0)
	check(t, id1 == 1 && err == nil)
	id2, err := l.Mkdir(0)
	check(t, id2 == 2 && err == nil)
	mustOK(t, l.Reserve(1, 60))
	b, e, r := lUsage(l, 0)
	check(t, b == 0 && e == 2 && r == 60)

	_, err = l.AddFile(2, 50)
	qe, ok := err.(*QuotaError)
	check(t, ok && qe.Dir == 0 && errors.Is(err, ErrBytesQuota))

	id3, err := l.AddFile(1, 50)
	check(t, id3 == 3 && err == nil)
	b, e, r = lUsage(l, 0)
	check(t, b == 50 && e == 3 && r == 10)

	id4, err := l.AddFile(1, 50)
	check(t, id4 == 4 && err == nil)
	b, e, r = lUsage(l, 0)
	check(t, b == 100 && e == 4 && r == 0)

	err = l.SetQuota(0, 99, -1)
	check(t, errors.Is(err, ErrBelowUsage))
	mustOK(t, l.SetQuota(0, 100, -1))

	_, err = l.Batch([]Op{{Kind: OpMkdir, P: 0}, {Kind: OpAddFile, P: 5, Size: 1}})
	be, ok := err.(*BatchError)
	check(t, ok && be.Index == 1 && errors.Is(err, ErrBatch) && errors.Is(err, ErrBytesQuota))

	id5, err := l.Mkdir(0)
	check(t, id5 == 5 && err == nil)
}

func lUsage(l *Ledger, d ID) (int64, int64, int64) {
	b, e, r, err := l.Usage(d)
	if err != nil {
		panic(err)
	}
	return b, e, r
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func check(t *testing.T, cond bool) {
	t.Helper()
	if !cond {
		t.Fatal("assertion failed")
	}
}
