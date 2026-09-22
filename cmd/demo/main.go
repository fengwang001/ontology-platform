// Command demo exercises the chunked-transfer streaming decoder.
// Run with: go run ./cmd/demo
package main

import (
	"bytes"
	"fmt"
	"sync"

	"ontology/chunked"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}

	wire, want := sample()

	// 1. Every possible split point yields the identical body.
	identical := true
	for size := 1; size <= len(wire); size++ {
		d, body, err := feed(wire, size)
		if err != nil || !d.Done() || !bytes.Equal(body, want) {
			identical = false
			break
		}
	}
	check("all split points decode identically", identical)

	// 2. Quoted extensions (with ;, = and escaped quote) are skipped.
	qwire := []byte("4;foo=\"a;b=c\\\"d\";z=y\r\nWiki\r\n0\r\n\r\n")
	_, qbody, qerr := feed(qwire, 2)
	check("quoted extensions with ; = and \\\" skipped", qerr == nil && string(qbody) == "Wiki")

	// 3. Message is incomplete until the trailer blank line arrives.
	part := []byte("4\r\nWiki\r\n0\r\nX: y\r\n")
	d := chunked.New()
	_, _ = d.Write(part)
	before := d.Done()
	_, _ = d.Write([]byte("\r\n"))
	check("complete only after trailer blank line", !before && d.Done())

	// 4. Writing after completion is rejected.
	n, werr := d.Write([]byte("X"))
	ce, _ := chunked.AsError(werr)
	check("write after completion rejected", n == 0 && ce != nil && ce.Kind == chunked.KindAlreadyDone)

	// 5. Six error classes are distinguishable with offsets.
	check("six error classes distinguishable", sixErrorsDistinct())

	// 6. Chunk boundary off by one byte is rejected.
	_, _, errExtra := feed([]byte("3\r\nabcd\r\n"), 1)
	_, _, errFew := feed([]byte("3\r\nab\r\n"), 1)
	_, _, errLF := feed([]byte("3\r\nabc\n"), 1)
	bad := func(e error) bool {
		c, ok := chunked.AsError(e)
		return ok && c.Kind == chunked.KindMissingCRLF
	}
	check("chunk boundary +/-1 and bare LF rejected", bad(errExtra) && bad(errFew) && bad(errLF))

	// 7. Limits reject immediately and the body is left intact.
	ld := chunked.NewWithLimits(chunked.Limits{MaxBody: 4})
	lerr := drain(ld, []byte("3\r\nabc\r\n2\r\nde\r\n0\r\n\r\n"))
	lce, _ := chunked.AsError(lerr)
	again, _ := ld.Write([]byte("z"))
	check("limit rejects immediately, body preserved",
		lce != nil && lce.Kind == chunked.KindBodyTooLarge &&
			string(ld.Body()) == "abc" && again == 0)

	// 8. Early Close reports four distinct incomplete states.
	check("four early-close states distinct", fourCloseStates())

	// 9. Concurrent independent decoders do not interfere.
	check("concurrent decoders isolated", concurrent(wire, want))

	fmt.Printf("TOTAL %d/%d\n", pass, total)
}

func feed(wire []byte, size int) (*chunked.Decoder, []byte, error) {
	d := chunked.New()
	off := 0
	for off < len(wire) {
		end := off + size
		if end > len(wire) {
			end = len(wire)
		}
		n, err := d.Write(wire[off:end])
		off += n
		if err != nil {
			return d, d.Body(), err
		}
	}
	return d, d.Body(), d.Close()
}

func drain(d *chunked.Decoder, wire []byte) error {
	_, err := d.Write(wire)
	if err == nil {
		err = d.Close()
	}
	return err
}

func sample() ([]byte, []byte) {
	var w bytes.Buffer
	var body []byte
	for _, c := range []string{"Hello, ", "world"} {
		fmt.Fprintf(&w, "%x\r\n%s\r\n", len(c), c)
		body = append(body, c...)
	}
	w.WriteString("0\r\nX-Trace: demo\r\n\r\n")
	return w.Bytes(), body
}

func sixErrorsDistinct() bool {
	type tc struct {
		wire   string
		limits chunked.Limits
		kind   chunked.Kind
	}
	cases := []tc{
		{"xg\r\n", chunked.Limits{}, chunked.KindNonHex},
		{"000a\r\n", chunked.Limits{MaxSizeLine: 4}, chunked.KindLineTooLong},
		{"4\r\nWikiX", chunked.Limits{}, chunked.KindMissingCRLF},
		{"4\r\nWiki\r", chunked.Limits{}, chunked.KindHalfCRLF},
		{"4;x=\"abc", chunked.Limits{}, chunked.KindUnterminatedQuote},
		{"0\r\nA: 1\r\nB: 2\r\n", chunked.Limits{MaxTrailers: 1}, chunked.KindTooManyTrailers},
	}
	seen := map[chunked.Kind]bool{}
	for _, c := range cases {
		d := chunked.New()
		if c.limits != (chunked.Limits{}) {
			d = chunked.NewWithLimits(c.limits)
		}
		err := drain(d, []byte(c.wire))
		ce, ok := chunked.AsError(err)
		if !ok || ce.Kind != c.kind {
			return false
		}
		seen[ce.Kind] = true
	}
	return len(seen) == 6
}

func fourCloseStates() bool {
	want := []chunked.Kind{
		chunked.KindIncompleteHeader,
		chunked.KindIncompleteData,
		chunked.KindIncompleteCRLF,
		chunked.KindIncompleteTrailers,
	}
	wires := []string{"1a;x", "4\r\nab", "4\r\nabcd", "0\r\nX: y\r\n"}
	for i, w := range wires {
		d := chunked.New()
		if err := drain(d, []byte(w)); err == nil {
			return false
		} else if ce, ok := chunked.AsError(err); !ok || ce.Kind != want[i] {
			return false
		}
	}
	return true
}

func concurrent(wire, want []byte) bool {
	var wg sync.WaitGroup
	ok := true
	var mu sync.Mutex
	for g := 0; g < 24; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, body, err := feed(wire, 1+id%len(wire))
			if err != nil || !bytes.Equal(body, want) {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	return ok
}
