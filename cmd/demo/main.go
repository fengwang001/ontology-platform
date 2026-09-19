package main

import (
	"fmt"
	"strings"
	"sync"

	"ontology"
)

var pass, fail int

func line(ok bool, format string, args ...any) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		fail++
	} else {
		pass++
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

func main() {
	strict := ontology.NewConverter(ontology.Strict)
	lenient := ontology.NewConverter(ontology.Lenient)
	tgt := ontology.Target{Kind: ontology.String, Nullable: true}

	// 1. Missing / explicit null / zero value under one target type.
	m, _ := strict.Coerce(map[string]any{}, "v", tgt)
	n, _ := strict.Coerce(map[string]any{"v": nil}, "v", tgt)
	z, _ := strict.Coerce(map[string]any{"v": ""}, "v", tgt)
	line(m.IsMissing() && n.IsExplicitNull() && z.IsZeroValue(ontology.String),
		"missing=%v explicitNull=%v zero=%v are three distinct results",
		m.Status, n.Status, z.Status)

	// 2. String overflow of int64 carries raw text.
	_, errOverflow := strict.CoerceValue("9223372036854775808", ontology.Target{Kind: ontology.Int64})
	oe := ontology.AsCoerceError(errOverflow)
	line(oe != nil && oe.Category == ontology.CatOverflow && strings.Contains(oe.Error(), "9223372036854775808"),
		"string overflow int64: %v", oe)

	// 3. Float -> int64 reports the dropped fraction.
	_, errFrac := strict.CoerceValue(3.75, ontology.Target{Kind: ontology.Int64})
	fe := ontology.AsCoerceError(errFrac)
	line(fe != nil && fe.Category == ontology.CatFractionLost && strings.Contains(fe.Error(), "0.75"),
		"float fraction lost: %v", fe)

	// 4. Same distorting input: strict errors, lenient succeeds with a record.
	distorted := 3.75
	_, sErr := strict.CoerceValue(distorted, ontology.Target{Kind: ontology.Int64})
	lo, lErr := lenient.CoerceValue(distorted, ontology.Target{Kind: ontology.Int64})
	line(sErr != nil && lErr == nil && lo.Int64 == 3 && len(lo.Records) == 1 &&
		lo.Records[0].Category == ontology.CatFractionLost,
		"strict=%v vs lenient value=%d records=%d", sErr, lo.Int64, len(lo.Records))

	// 5. "yes" and 2 both fail boolean conversion.
	_, e1 := strict.CoerceValue("yes", ontology.Target{Kind: ontology.Bool})
	_, e2 := strict.CoerceValue(2, ontology.Target{Kind: ontology.Bool})
	line(ontology.AsCoerceError(e1).Category == ontology.CatInvalidBool &&
		ontology.AsCoerceError(e2).Category == ontology.CatInvalidBool,
		"\"yes\" -> %v ; 2 -> %v", e1, e2)

	// 6. Slice failure locates element index 2.
	_, eSlice := strict.CoerceValue([]any{int64(1), int64(2), "boom"}, ontology.Target{Kind: ontology.Int64s})
	se := ontology.AsCoerceError(eSlice)
	line(se != nil && se.Index == 2 && se.ElementCategory == ontology.CatMalformedNumber,
		"slice element failure: %v", se)

	// 7. Concurrent lenient calls keep records independent.
	clean := concurrentRace(lenient)
	line(clean, "concurrent calls: degradation records never cross-talk (64 goroutines x200)")

	fmt.Printf("TOTAL %d OK, %d FAIL\n", pass, fail)
}

func concurrentRace(c *ontology.Converter) bool {
	var wg sync.WaitGroup
	bad := make(chan bool, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			text := "12345678901234567890" + fmt.Sprintf("%d", g)
			in := []any{int64(1), text}
			for i := 0; i < 200; i++ {
				o, err := c.Coerce(map[string]any{"v": in}, "v", ontology.Target{Kind: ontology.Int64s})
				if err != nil || len(o.Records) != 1 || o.Records[0].From != text {
					bad <- true
					return
				}
				o.Slice = append(o.Slice, int64(999))
			}
		}(g)
	}
	wg.Wait()
	close(bad)
	return len(bad) == 0
}
