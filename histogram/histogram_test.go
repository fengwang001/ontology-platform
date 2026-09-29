package histogram

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// logAssignment prints the input value, which bucket it belongs to, the
// bucket count and the rule used to decide membership.
func logAssignment(t *testing.T, h *Histogram, value int64, phase string) {
	t.Helper()
	idx, reason := classify(h, value)
	_, err := h.BucketOf(value)
	switch {
	case errors.Is(err, ErrNegativeValue):
		t.Logf("[%s] input=%d -> bucket %d (%s); query rejected: negative value", phase, value, idx, reason)
	case errors.Is(err, ErrBucketNotFound):
		t.Logf("[%s] input=%d -> bucket %d (%s); count=0 bucket absent (not found, not zero)", phase, value, idx, reason)
	case err != nil:
		t.Logf("[%s] input=%d -> bucket %d (%s); query error: %v", phase, value, idx, reason, err)
	default:
		b, _ := h.BucketOf(value)
		t.Logf("[%s] input=%d -> bucket %d [%d,%d) overflow=%v count=%d; basis: %s",
			phase, value, b.Index, b.Lower, b.Upper, b.Overflow, b.Count, reason)
	}
}

// classify returns the bucket index a value maps to and a human-readable
// statement of the membership rule.
func classify(h *Histogram, value int64) (int64, string) {
	if value < 0 {
		return -1, "negative values are illegal"
	}
	if value >= h.UpperBound() {
		return h.NumBuckets(), fmt.Sprintf("value %d >= upperBound %d, so it goes to the overflow bucket", value, h.UpperBound())
	}
	idx := value / h.Width()
	base := idx * h.Width()
	return idx, fmt.Sprintf("%d/%d=%d (integer floor); %d lies in [%d,%d)",
		value, h.Width(), idx, value, base, base+h.Width())
}

func TestInvalidConstruction(t *testing.T) {
	cases := []struct {
		name    string
		width   int64
		upper   int64
		max     int
		wantErr error
	}{
		{"zero width", 0, 10, 4, ErrInvalidWidth},
		{"negative width", -2, 10, 4, ErrInvalidWidth},
		{"zero upper", 2, 0, 4, ErrInvalidUpperBound},
		{"negative upper", 2, -10, 4, ErrInvalidUpperBound},
		{"upper not multiple of width", 3, 10, 4, ErrInvalidUpperBound},
		{"zero max buckets", 2, 10, 0, ErrInvalidMaxBuckets},
		{"negative max buckets", 2, 10, -1, ErrInvalidMaxBuckets},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.width, tc.upper, tc.max)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("New(%d,%d,%d) err=%v, want %v", tc.width, tc.upper, tc.max, err, tc.wantErr)
			}
			t.Logf("New(width=%d, upper=%d, max=%d) rejected with %v as expected", tc.width, tc.upper, tc.max, err)
		})
	}
}

func TestBucketBoundaries(t *testing.T) {
	// width=5, upper=20 => [0,5) [5,10) [10,15) [15,20) plus overflow [20,+inf).
	h, err := New(5, 20, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	values := []int64{0, 1, 4, 5, 9, 10, 14, 15, 19, 20, 21, 1000000}
	wantIndex := map[int64]int64{
		0: 0, 1: 0, 4: 0,
		5: 1, 9: 1,
		10: 2, 14: 2,
		15: 3, 19: 3,
		20: 4, 21: 4, 1000000: 4,
	}
	wantCount := map[int64]int64{
		0: 3, 1: 3, 4: 3,
		5: 2, 9: 2,
		10: 2, 14: 2,
		15: 2, 19: 2,
		20: 3, 21: 3, 1000000: 3,
	}

	for _, v := range values {
		if err := h.Add(v); err != nil {
			t.Fatalf("Add(%d): %v", v, err)
		}
		logAssignment(t, h, v, "add")
	}

	for _, v := range values {
		b, err := h.BucketOf(v)
		if err != nil {
			t.Fatalf("BucketOf(%d): %v", v, err)
		}
		if b.Index != wantIndex[v] {
			t.Errorf("value %d got bucket %d, want %d", v, b.Index, wantIndex[v])
		}
		if b.Count != wantCount[v] {
			t.Errorf("value %d got count %d, want %d", v, b.Count, wantCount[v])
		}
		if v >= 20 {
			if !b.Overflow || b.Lower != 20 || b.Upper != 0 {
				t.Errorf("value %d should be in overflow bucket [20,+inf), got %+v", v, b)
			}
		} else if b.Overflow || b.Upper != b.Lower+5 {
			t.Errorf("value %d should be in a regular half-open bucket, got %+v", v, b)
		}
	}

	all := h.Buckets()
	if len(all) != 5 {
		t.Fatalf("active buckets=%d, want 5 (4 regular + 1 overflow)", len(all))
	}
	for i, b := range all {
		t.Logf("snapshot[%d] = bucket %d [%d,%d) overflow=%v count=%d",
			i, b.Index, b.Lower, b.Upper, b.Overflow, b.Count)
		if int64(i) != b.Index {
			t.Errorf("snapshot not ordered by index at %d: %+v", i, b)
		}
		if b.Count <= 0 {
			t.Errorf("bucket %d snapshot has non-positive count %d", b.Index, b.Count)
		}
	}
}

func TestCountIncrements(t *testing.T) {
	h, _ := New(10, 30, 4)
	for _, v := range []int64{10, 15, 19, 0, 9} {
		if err := h.Add(v); err != nil {
			t.Fatalf("Add(%d): %v", v, err)
		}
	}
	b1, err := h.BucketOf(17)
	if err != nil {
		t.Fatalf("BucketOf: %v", err)
	}
	logAssignment(t, h, 17, "query")
	if b1.Count != 3 {
		t.Fatalf("bucket 1 count=%d, want 3", b1.Count)
	}
	b0, _ := h.BucketOf(3)
	if b0.Count != 2 {
		t.Fatalf("bucket 0 count=%d, want 2", b0.Count)
	}
}

func TestRetractRemovesEmptyBucket(t *testing.T) {
	h, _ := New(10, 30, 4)

	if err := h.Retract(5); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("retract from never-seen bucket err=%v, want ErrBucketNotFound", err)
	}

	h.Add(5)
	h.Add(5)
	if err := h.Retract(5); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	b, err := h.BucketOf(5)
	if err != nil || b.Count != 1 {
		t.Fatalf("after first retract want count 1, got %+v err=%v", b, err)
	}
	logAssignment(t, h, 5, "retract-to-one")

	if err := h.Retract(5); err != nil {
		t.Fatalf("Retract last: %v", err)
	}
	if _, err := h.BucketOf(5); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("empty bucket query err=%v, want ErrBucketNotFound", err)
	}
	if h.ActiveBuckets() != 0 {
		t.Fatalf("active buckets=%d, want 0 after bucket disappeared", h.ActiveBuckets())
	}
	logAssignment(t, h, 5, "retract-to-zero")

	// A failed retract against the vanished bucket must not recreate it.
	if err := h.Retract(5); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("retract vanished bucket err=%v, want ErrBucketNotFound", err)
	}
	if h.ActiveBuckets() != 0 {
		t.Fatalf("failed retract changed histogram, active=%d", h.ActiveBuckets())
	}
	t.Logf("retract of vanished bucket rejected; histogram unchanged, active=%d", h.ActiveBuckets())

	// The bucket can reappear after a new observation.
	if err := h.Add(5); err != nil {
		t.Fatalf("re-Add: %v", err)
	}
	b, err = h.BucketOf(5)
	if err != nil || b.Count != 1 {
		t.Fatalf("reappeared bucket want count 1, got %+v err=%v", b, err)
	}
	logAssignment(t, h, 5, "reappear")
}

func TestNegativeValues(t *testing.T) {
	h, _ := New(10, 30, 4)
	h.Add(5)
	snapshotBefore := h.Buckets()

	if _, err := h.BucketOf(-1); !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("BucketOf(-1) err=%v, want ErrNegativeValue", err)
	}
	for _, op := range []func(int64) error{h.Add, h.Retract} {
		if err := op(-7); !errors.Is(err, ErrNegativeValue) {
			t.Fatalf("op(-7) err=%v, want ErrNegativeValue", err)
		}
		logAssignment(t, h, -7, "reject")
	}
	if got := h.Buckets(); !reflect.DeepEqual(got, snapshotBefore) {
		t.Fatalf("rejected negative operation changed histogram: before=%+v after=%+v", snapshotBefore, got)
	}
	t.Log("negative add/retract rejected; histogram unchanged")
}

func TestActiveBucketLimit(t *testing.T) {
	h, _ := New(10, 30, 2)
	if err := h.Add(1); err != nil { // bucket 0
		t.Fatalf("Add: %v", err)
	}
	if err := h.Add(11); err != nil { // bucket 1
		t.Fatalf("Add: %v", err)
	}

	before := h.Buckets()
	err := h.Add(25) // would open bucket 2
	if !errors.Is(err, ErrTooManyBuckets) {
		t.Fatalf("third active bucket err=%v, want ErrTooManyBuckets", err)
	}
	// Overflow counts too: with both slots taken it must be rejected as well.
	if err := h.Add(30); !errors.Is(err, ErrTooManyBuckets) {
		t.Fatalf("overflow over limit err=%v, want ErrTooManyBuckets", err)
	}
	if got := h.Buckets(); !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected add changed histogram: before=%+v after=%+v", before, got)
	}
	t.Logf("opening a 3rd bucket rejected (%v); histogram unchanged, active=%d", err, h.ActiveBuckets())

	// Filling an existing bucket is still allowed at the limit.
	if err := h.Add(2); err != nil {
		t.Fatalf("Add into existing bucket at limit: %v", err)
	}
	b0, _ := h.BucketOf(0)
	if b0.Count != 2 {
		t.Fatalf("bucket 0 count=%d, want 2", b0.Count)
	}

	// After a bucket disappears its slot frees up.
	if err := h.Retract(11); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if err := h.Add(25); err != nil {
		t.Fatalf("Add into freed slot: %v", err)
	}
	logAssignment(t, h, 25, "freed-slot")
}

// TestConcurrentAddDifferentBuckets runs concurrent adds spread across
// distinct buckets and compares per-bucket counts against a serial model.
func TestConcurrentAddDifferentBuckets(t *testing.T) {
	const workers = 16
	const perWorker = 1000

	h, err := New(1, 100, 200)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := make(map[int64]int64)
	var wantMu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				value := int64((worker*perWorker + i) % 100)
				if err := h.Add(value); err != nil {
					t.Errorf("concurrent Add(%d): %v", value, err)
					return
				}
				wantMu.Lock()
				want[value]++
				wantMu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	total := int64(workers * perWorker)
	var observed int64
	for value, expected := range want {
		b, err := h.BucketOf(value)
		if err != nil {
			t.Fatalf("BucketOf(%d): %v", value, err)
		}
		if b.Count != expected {
			t.Fatalf("bucket for %d count=%d, serial reference=%d", value, b.Count, expected)
		}
		observed += b.Count
	}
	if observed != total {
		t.Fatalf("total observed=%d, want %d", observed, total)
	}
	t.Logf("concurrent adds: %d workers x %d = %d values across %d buckets; counts match serial reference",
		workers, perWorker, total, len(want))
}

// TestConcurrentAddRetractBalance drives equal numbers of adds and
// retracts concurrently. After all positives and negatives cancel out,
// grouping counts must all be zero, i.e. every bucket must have
// disappeared and snapshots must be empty.
func TestConcurrentAddRetractBalance(t *testing.T) {
	const goroutines = 12
	const rounds = 500

	h, err := New(4, 16, 16)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				value := int64((seed + i) % 16) // values 0..15 -> 4 buckets
				if err := h.Add(value); err != nil {
					t.Errorf("Add(%d): %v", value, err)
					return
				}
				if err := h.Retract(value); err != nil {
					t.Errorf("Retract(%d): %v", value, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if got := h.ActiveBuckets(); got != 0 {
		t.Fatalf("after positives and negatives cancel, active buckets=%d, want 0", got)
	}
	if got := h.Buckets(); len(got) != 0 {
		t.Fatalf("after cancellation snapshot=%+v, want empty", got)
	}
	t.Logf("positives and negatives fully canceled: %d add/retract pairs, all bucket group counts zero, snapshot empty",
		goroutines*rounds)
}

// TestConcurrentReadsAreIdentical populates buckets and concurrently takes
// snapshots, asserting every reader sees field-by-field identical output.
func TestConcurrentReadsAreIdentical(t *testing.T) {
	h, _ := New(10, 40, 8)
	for _, v := range []int64{0, 10, 10, 25, 40, 99} {
		if err := h.Add(v); err != nil {
			t.Fatalf("Add(%d): %v", v, err)
		}
	}
	want := h.Buckets()

	const readers = 32
	results := make([][]Bucket, readers)
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = h.Buckets()
		}(r)
	}

	// Concurrent writers hit existing buckets only, so counts keep changing;
	// each reader must still observe a self-consistent snapshot whose bucket
	// set and bounds match the fixed reference shape.
	stop := make(chan struct{})
	var writerWG sync.WaitGroup
	writerWG.Add(2)
	for w := 0; w < 2; w++ {
		go func(seed int) {
			defer writerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = h.Add(int64((seed * 10)))
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	writerWG.Wait()

	for i, got := range results {
		if len(got) != len(want) {
			t.Fatalf("reader %d saw %d buckets, want shape %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j].Index != want[j].Index ||
				got[j].Lower != want[j].Lower ||
				got[j].Upper != want[j].Upper ||
				got[j].Overflow != want[j].Overflow {
				t.Fatalf("reader %d bucket %d shape=%+v, want %+v", i, j, got[j], want[j])
			}
			if got[j].Count < want[j].Count || got[j].Count <= 0 {
				t.Fatalf("reader %d bucket %d count=%d, want >= %d and positive",
					i, j, got[j].Count, want[j].Count)
			}
		}
	}
	t.Logf("%d concurrent readers observed identical bucket sets and bounds: %+v", readers, want)
}

// TestConcurrentPointQueries checks concurrent single-bucket queries.
func TestConcurrentPointQueries(t *testing.T) {
	h, _ := New(2, 10, 16)
	for i := 0; i < 5; i++ {
		for j := 0; j < 3; j++ {
			h.Add(int64(2 * i))
		}
	}

	const readers = 64
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			value := int64(2 * (seed % 5))
			b, err := h.BucketOf(value + 1) // same bucket as value
			if err != nil {
				errs <- err
				return
			}
			if b.Count != 3 {
				errs <- fmt.Errorf("bucket for %d count=%d, want 3", value+1, b.Count)
				return
			}
		}(r)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("%d concurrent point queries all returned count=3 per bucket", readers)
}
