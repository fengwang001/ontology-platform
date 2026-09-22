// Command demo exercises the order-preserving encoder end to end.
// It prints one OK/FAIL line per check and a final summary.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"

	"ontology"
)

var checks int

func report(ok bool, format string, args ...any) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	} else {
		checks++
	}
	fmt.Printf(mark+" "+format+"\n", args...)
}

func main() {
	enc := ontology.NewEncoder(nil)

	// 1. Sorting rows by encoded bytes matches sorting by value.
	rows := [][]any{
		{int64(3), "ab", nil}, {int64(3), "a", int64(9)},
		{float64(3.5), "a", nil}, {nil, "zzz", int64(1)},
		{int64(-2), "", nil}, {float64(-2.5), "x", nil},
		{int64(3), "a", int64(4)}, {float64(0), "q", nil},
	}
	byBytes := append([][]any{}, rows...)
	sort.Slice(byBytes, func(i, j int) bool {
		return bytes.Compare(enc.Encode(byBytes[i]), enc.Encode(byBytes[j])) < 0
	})
	byValue := append([][]any{}, rows...)
	sort.Slice(byValue, func(i, j int) bool {
		return ontology.CompareRows(byValue[i], byValue[j]) < 0
	})
	same := true
	for i := range byBytes {
		if ontology.CompareRows(byBytes[i], byValue[i]) != 0 {
			same = false
		}
	}
	report(same, "sort-by-bytes matches sort-by-value (%d rows)", len(rows))

	// 2. int64(3) sorts before float64(3.5) in byte order.
	c := bytes.Compare(enc.Encode([]any{int64(3)}), enc.Encode([]any{3.5}))
	report(c < 0, "int64(3) < float64(3.5) in byte order")

	// 3. "a" sorts before "ab".
	c = bytes.Compare(enc.Encode([]any{"a"}), enc.Encode([]any{"ab"}))
	report(c < 0, `%q < %q in byte order`, "a", "ab")

	// 4. A string containing 0x00 round-trips byte-exactly.
	back, err := ontology.Decode(nil, enc.Encode([]any{"a\x00b\xff"}))
	report(err == nil && back[0] == "a\x00b\xff", "string with 0x00/0xFF round-trips")

	// 5. Descending flips value order but nil stays first.
	desc := ontology.NewEncoder([]bool{true})
	flip := bytes.Compare(desc.Encode([]any{int64(1)}), desc.Encode([]any{int64(2)})) > 0
	nilFirst := bytes.Compare(desc.Encode([]any{nil}), desc.Encode([]any{math.Inf(-1)})) < 0
	report(flip && nilFirst, "descending flips order, nil still first")

	// 6. NaN encodes like nil and is counted.
	nanEnc := ontology.NewEncoder(nil)
	nanEnc.Encode([]any{math.NaN(), int64(1)})
	nanEnc.Encode([]any{math.NaN()})
	report(nanEnc.NaNCount() == 2, "NaN counted like nil (count=%d)", nanEnc.NaNCount())

	// 7. Truncated input reports the failing key index.
	full := enc.Encode([]any{int64(1), "abc", 2.5})
	_, err = ontology.Decode([]bool{false, false, false}, full[:len(full)-2])
	var de *ontology.DecodeError
	ok := errors.As(err, &de) && errors.Is(err, ontology.ErrTruncated)
	report(ok, "truncated input -> key %d: %v", de.KeyIndex, de.Err)

	// 8. Small integers encode well below 8 bytes.
	n := enc.EncodedLen([]any{int64(42)})
	report(n < 8, "int64(42) encodes to %d bytes (< 8)", n)

	fmt.Printf("TOTAL: %d/8 checks passed\n", checks)
	if checks != 8 {
		os.Exit(1)
	}
}
