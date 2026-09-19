package ontology

import (
	"strconv"
	"sync"
	"testing"
)

// One shared stateless converter hammered by many goroutines. Each
// goroutine uses a distinct overflow text and must only ever observe
// its own records and result slices. Run with -race.
func TestConverterConcurrentNoCrossTalk(t *testing.T) {
	c := NewConverter(Lenient)
	const goroutines = 64

	var wg sync.WaitGroup
	errs := make(chan string, goroutines)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			// All goroutines overflow int64; the distinct digit prefix
			// makes each raw text unique so cross-talk is observable.
			myText := "12345678901234567890" + strconv.Itoa(g)
			in := []any{int64(1), myText, 3.75, "yes"}
			for i := 0; i < 200; i++ {
				out, err := c.Coerce(map[string]any{"v": in}, "v", Target{Kind: Int64s})
				if err != nil {
					errs <- "lenient slice must not error"
					return
				}
				if len(out.Records) != 3 {
					errs <- "wrong record count"
					return
				}
				if out.Records[0].Category != CatOverflow ||
					out.Records[0].From != myText || out.Records[0].Index != 1 {
					errs <- "overflow record cross-talk: " + out.Records[0].String()
					return
				}
				if out.Records[1].Category != CatFractionLost || out.Records[1].Index != 2 ||
					out.Records[1].From != 3.75 {
					errs <- "fraction record cross-talk"
					return
				}
				if out.Records[2].Category != CatMalformedNumber || out.Records[2].Index != 3 {
					errs <- "element-3 record cross-talk"
					return
				}
				if len(out.Slice) != 1 || out.Slice[0] != int64(1) {
					errs <- "result slice cross-talk"
					return
				}
				// Mutate the returned slice; next iteration must be unaffected.
				out.Slice[0] = int64(999)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}

func TestConverterReturnsIndependentSlices(t *testing.T) {
	c := NewConverter(Lenient)
	in := []any{int64(1), "bad"}
	o1, _ := c.CoerceValue(in, Target{Kind: Int64s})
	o2, _ := c.CoerceValue(in, Target{Kind: Int64s})
	o1.Slice = append(o1.Slice, int64(42))
	o1.Records[0].From = "tampered"
	if len(o2.Slice) != 1 || o2.Records[0].From != "bad" {
		t.Fatalf("calls share backing arrays: %+v %+v", o1, o2)
	}
}
