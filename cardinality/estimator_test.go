package cardinality

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// findKey returns the first key of the form "prefix-N" whose hash maps
// to the given register with a rank satisfying accept.
func findKey(prefix string, precision uint8, register uint32, accept func(rank uint8) bool) (string, uint8) {
	for i := 0; ; i++ {
		key := fmt.Sprintf("%s-%d", prefix, i)
		index, rank := locate(hashKey(key), precision)
		if index == register && accept(rank) {
			return key, rank
		}
	}
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name      string
		precision uint8
		threshold int
		want      error
	}{
		{"precision too low", MinPrecision - 1, 10, ErrInvalidPrecision},
		{"precision too high", MaxPrecision + 1, 10, ErrInvalidPrecision},
		{"threshold zero", MinPrecision, 0, ErrInvalidThreshold},
		{"threshold negative", MinPrecision, -5, ErrInvalidThreshold},
	}
	for _, tc := range cases {
		if _, err := New(tc.precision, tc.threshold); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		t.Logf("op=new precision=%d threshold=%d decision=reject reason=%v",
			tc.precision, tc.threshold, tc.want)
	}
	if _, err := New(MinPrecision, 1); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
}

func TestEmptyKeyRejected(t *testing.T) {
	est, err := New(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := est.Add(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("add empty: got %v, want ErrEmptyKey", err)
	}
	if err := est.Add("a"); err != nil {
		t.Fatal(err)
	}
	before := est.Estimate()
	if err := est.Remove(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("remove empty: got %v, want ErrEmptyKey", err)
	}
	if got := est.Estimate(); got != before {
		t.Fatalf("estimate changed after failed remove: got %d, want %d", got, before)
	}
	t.Logf("op=remove key=%q mode=%s estimate=%d decision=reject reason=%v basis=empty-key",
		"", est.Mode(), est.Estimate(), ErrEmptyKey)
	if err := est.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestSparseExactAndConversionTiming(t *testing.T) {
	const threshold = 8
	est, err := New(4, threshold)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < threshold; i++ {
		key := fmt.Sprintf("k-%d", i)
		if err := est.Add(key); err != nil {
			t.Fatal(err)
		}
		if est.Mode() != ModeSparse {
			t.Fatalf("mode=%s after %d keys, want sparse", est.Mode(), i+1)
		}
		if got := est.Estimate(); got != uint64(i+1) {
			t.Fatalf("estimate=%d after %d keys, want exact %d", got, i+1, i+1)
		}
	}
	t.Logf("op=add count=%d mode=%s estimate=%d decision=stay-sparse basis=len(%d)<=threshold(%d)",
		threshold, est.Mode(), est.Estimate(), threshold, threshold)

	if err := est.Add("k-overflow"); err != nil {
		t.Fatal(err)
	}
	if est.Mode() != ModeDense {
		t.Fatalf("mode=%s after threshold+1 keys, want dense", est.Mode())
	}
	t.Logf("op=add key=%q mode=%s estimate=%d decision=convert basis=len(%d)>threshold(%d)",
		"k-overflow", est.Mode(), est.Estimate(), threshold+1, threshold)

	for i := 0; i < threshold; i++ {
		if err := est.Remove(fmt.Sprintf("k-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if est.Mode() != ModeDense {
		t.Fatalf("mode=%s after removals below threshold, want dense (no fallback)", est.Mode())
	}
	t.Logf("op=remove count=%d mode=%s len=%d decision=no-fallback basis=conversion-is-one-way",
		threshold, est.Mode(), est.Len())
	if err := est.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestRemovalFallback(t *testing.T) {
	const precision = 4
	est, err := New(precision, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Two keys in register 0 with distinct ranks: removing the higher
	// rank must drop the register to the remaining rank, not to zero.
	keyLow, rankLow := findKey("low", precision, 0, func(r uint8) bool { return r == 1 })
	keyHigh, rankHigh := findKey("high", precision, 0, func(r uint8) bool { return r >= 3 })
	t.Logf("op=setup reg[0] keys=%q(rank=%d),%q(rank=%d) basis=deterministic-hash",
		keyLow, rankLow, keyHigh, rankHigh)

	if err := est.Add(keyLow); err != nil {
		t.Fatal(err)
	}
	if err := est.Add(keyHigh); err != nil {
		t.Fatal(err)
	}
	if est.Mode() != ModeDense {
		t.Fatalf("mode=%s, want dense", est.Mode())
	}
	if got := est.Registers()[0]; got != rankHigh {
		t.Fatalf("reg[0]=%d, want max rank %d", got, rankHigh)
	}
	t.Logf("op=add key=%q mode=%s reg[0]=%d decision=raise basis=max(%d,%d)",
		keyHigh, est.Mode(), rankHigh, rankLow, rankHigh)

	if err := est.Remove(keyHigh); err != nil {
		t.Fatal(err)
	}
	if got := est.Registers()[0]; got != rankLow {
		t.Fatalf("reg[0]=%d after removing max rank, want fallback to %d", got, rankLow)
	}
	t.Logf("op=remove key=%q mode=%s reg[0]=%d decision=fallback basis=remaining-max-rank=%d",
		keyHigh, est.Mode(), rankLow, rankLow)

	if err := est.Remove(keyLow); err != nil {
		t.Fatal(err)
	}
	if got := est.Registers()[0]; got != 0 {
		t.Fatalf("reg[0]=%d after removing last rank, want 0", got)
	}
	t.Logf("op=remove key=%q mode=%s reg[0]=0 decision=clear basis=no-remaining-ranks",
		keyLow, est.Mode())
	if err := est.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestRemoveNonExistent(t *testing.T) {
	est, err := New(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b", "c"} {
		if err := est.Add(key); err != nil {
			t.Fatal(err)
		}
	}
	if est.Mode() != ModeDense {
		t.Fatalf("mode=%s, want dense", est.Mode())
	}
	beforeEstimate := est.Estimate()
	beforeRegs := est.Registers()

	err = est.Remove("ghost")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("got %v, want ErrKeyNotFound", err)
	}
	if got := est.Estimate(); got != beforeEstimate {
		t.Fatalf("estimate changed: got %d, want %d", got, beforeEstimate)
	}
	for i, rank := range est.Registers() {
		if rank != beforeRegs[i] {
			t.Fatalf("reg[%d] changed: got %d, want %d", i, rank, beforeRegs[i])
		}
	}
	if est.Len() != 3 {
		t.Fatalf("len=%d, want 3", est.Len())
	}
	t.Logf("op=remove key=%q mode=%s estimate=%d decision=reject reason=%v basis=key-not-in-set",
		"ghost", est.Mode(), est.Estimate(), err)

	sparse, err := New(4, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := sparse.Add("x"); err != nil {
		t.Fatal(err)
	}
	if err := sparse.Remove("ghost"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("sparse: got %v, want ErrKeyNotFound", err)
	}
	if got := sparse.Estimate(); got != 1 {
		t.Fatalf("sparse estimate=%d, want 1", got)
	}
	t.Logf("op=remove key=%q mode=%s estimate=%d decision=reject reason=%v basis=key-not-in-set",
		"ghost", sparse.Mode(), sparse.Estimate(), ErrKeyNotFound)
	if err := est.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestDuplicateAddIdempotent(t *testing.T) {
	est, err := New(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := est.Add("a"); err != nil {
			t.Fatal(err)
		}
	}
	if est.Len() != 1 || est.Estimate() != 1 {
		t.Fatalf("len=%d estimate=%d, want 1/1", est.Len(), est.Estimate())
	}
	t.Logf("op=add key=%q mode=%s len=%d decision=dedup basis=key-already-present",
		"a", est.Mode(), est.Len())
	if err := est.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if err := est.Remove("a"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("second remove: got %v, want ErrKeyNotFound", err)
	}
}

func TestConcurrentAddsMatchSerial(t *testing.T) {
	const n = 2000
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}
	serial, err := New(6, 16)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if err := serial.Add(key); err != nil {
			t.Fatal(err)
		}
	}
	want := serial.Estimate()

	concurrent, err := New(6, 16)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			if err := concurrent.Add(key); err != nil {
				t.Errorf("add %q: %v", key, err)
			}
		}(key)
	}
	wg.Wait()
	got := concurrent.Estimate()
	t.Logf("op=add-concurrent keys=%d mode=%s estimate=%d serial=%d decision=equal basis=deterministic-hash",
		n, concurrent.Mode(), got, want)
	if got != want {
		t.Fatalf("concurrent estimate=%d, want serial %d", got, want)
	}
	if err := concurrent.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestRemovalRestoresEstimate(t *testing.T) {
	est, err := New(6, 8)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if err := est.Add(fmt.Sprintf("base-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := est.Estimate()
	snapshotRegs := est.Registers()

	for i := 0; i < 300; i++ {
		if err := est.Add(fmt.Sprintf("extra-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 300; i++ {
		if err := est.Remove(fmt.Sprintf("extra-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := est.Estimate(); got != snapshot {
		t.Fatalf("estimate=%d after removals, want restored %d", got, snapshot)
	}
	for i, rank := range est.Registers() {
		if rank != snapshotRegs[i] {
			t.Fatalf("reg[%d]=%d, want restored %d", i, rank, snapshotRegs[i])
		}
	}
	t.Logf("op=remove-extras mode=%s estimate=%d snapshot=%d decision=restored basis=rank-counts-reversible",
		est.Mode(), est.Estimate(), snapshot)
	if err := est.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}
