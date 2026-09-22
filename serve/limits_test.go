package serve

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/multipart"
	"ontology/source"
)

// trapRand yields the same raw bytes forever, so the generated boundary
// candidate is always the same string.
type trapRand struct{ raw []byte }

func (r trapRand) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.raw[i%len(r.raw)]
	}
	return len(p), nil
}

// limitFixture builds a 1000-byte source whose bytes 2..43 contain a trap
// boundary string, plus the rand that regenerates exactly that boundary.
func limitFixture() (*Assembler, []byte) {
	raw := []byte("0123456789abcdef")
	trap := multipart.BoundaryPrefix + hex.EncodeToString(raw) // 41 bytes
	data := make([]byte, 1000)
	for i := range data {
		data[i] = 'x'
	}
	copy(data[2:], trap)
	a := &Assembler{
		Src:    source.Bytes(data),
		Limits: Limits{MaxRanges: 2, MaxBytes: 500, MaxBoundaryTry: 1},
		Rand:   trapRand{raw: raw},
	}
	return a, data
}

func TestLimitsAreDistinguishableAndStateless(t *testing.T) {
	a, _ := limitFixture()
	// Baseline: a valid build works.
	before := buildOK(t, a, "bytes=0-3")
	beforeBody := bodyOf(t, before)

	// 1) too many ranges (limit is 2).
	_, err1 := a.Build("bytes=0-1, 2-3, 6-7")
	// 2) response too large (1000 bytes > 500 limit).
	_, err2 := a.Build("bytes=0-999")
	// 3) boundary retries exhausted: the only candidate is the trap,
	// which occurs in the requested content (two disjoint ranges).
	_, err3 := a.Build("bytes=0-42, 100-109")

	if !errors.Is(err1, ErrTooManyRanges) {
		t.Fatalf("err1=%v, want ErrTooManyRanges", err1)
	}
	if !errors.Is(err2, ErrTooLarge) {
		t.Fatalf("err2=%v, want ErrTooLarge", err2)
	}
	if !errors.Is(err3, multipart.ErrBoundaryExhausted) {
		t.Fatalf("err3=%v, want ErrBoundaryExhausted", err3)
	}
	// The three kinds are mutually distinguishable.
	if errors.Is(err1, ErrTooLarge) || errors.Is(err1, multipart.ErrBoundaryExhausted) ||
		errors.Is(err2, ErrTooManyRanges) || errors.Is(err2, multipart.ErrBoundaryExhausted) ||
		errors.Is(err3, ErrTooManyRanges) || errors.Is(err3, ErrTooLarge) {
		t.Fatal("limit errors are not mutually distinguishable")
	}
	// Rejection changed nothing: the assembler still builds, and the
	// earlier response is untouched.
	after := buildOK(t, a, "bytes=0-3")
	if string(bodyOf(t, after)) != string(beforeBody) {
		t.Fatal("assembler state changed after rejections")
	}
	if before.Written() != int64(len(beforeBody)) {
		t.Fatal("earlier response state changed after rejections")
	}
}

func TestQueriesAreStableAndNonMutating(t *testing.T) {
	a := &Assembler{Src: source.Bytes([]byte("0123456789")), Rand: zeroRand{}}
	r := buildOK(t, a, "bytes=0-2, 5-6")
	type snapshot struct {
		ranges string
		total  int64
		wrote  int64
		multi  bool
	}
	snap := func() snapshot {
		return snapshot{
			ranges: fmt.Sprint(r.Ranges()),
			total:  r.TotalBytes(),
			wrote:  r.Written(),
			multi:  r.Multipart(),
		}
	}
	if s1, s2 := snap(), snap(); s1 != s2 {
		t.Fatalf("two consecutive queries differ: %+v vs %+v", s1, s2)
	}
	if !r.Multipart() || r.Written() != 0 || r.TotalBytes() == 0 {
		t.Fatalf("unexpected query values: %+v", snap())
	}
	// Not assembled yet: zero value queries are all zero.
	var zero Response
	if zero.Ranges() != nil || zero.TotalBytes() != 0 || zero.Written() != 0 || zero.Multipart() {
		t.Fatal("zero-value response queries must be zero")
	}
}

func TestConcurrentAssembly(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			payload := strings.Repeat(string(rune('a'+g%26)), 256)
			a := &Assembler{Src: source.Bytes([]byte(payload))}
			r, err := a.Build("bytes=0-9, 100-199, -20")
			if err != nil {
				errs <- fmt.Errorf("g=%d build: %w", g, err)
				return
			}
			w := &chunkWriter{max: 7}
			for {
				_, err := r.WriteSome(w)
				if err != nil {
					break
				}
			}
			if r.Written() != r.TotalBytes() {
				errs <- fmt.Errorf("g=%d written=%d total=%d", g, r.Written(), r.TotalBytes())
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
