package lzw

import "fmt"

// naiveEvent is one step of the step-by-step reference implementation: a code
// emitted at a given width, with a human-readable decision note.
type naiveEvent struct {
	code  int
	width int
	note  string
}

// naiveEncode is a direct, unoptimized transcription of the encoding rules.
// It returns both the (code, width) events (including notes for logging) and
// the packed bytes. phantomAtClose reports that the phantom next-free number
// counted at Close triggers a width bump.
func naiveEncode(in []byte) (events []naiveEvent, packed []byte, phantomAtClose bool) {
	table := map[string]int{}
	reset := func() {
		table = map[string]int{}
		for i := 0; i < 256; i++ {
			table[string([]byte{byte(i)})] = i
		}
	}
	reset()
	next, width := firstFree, minWidth
	events = append(events, naiveEvent{clearCode, minWidth, "leading clear"})
	w := ""
	emit := func(code int, why string) {
		events = append(events, naiveEvent{code, width, why})
	}
	for _, b := range in {
		c := string([]byte{b})
		wc := w + c
		if len(w) == 0 {
			w = wc
			continue
		}
		if _, ok := table[wc]; ok {
			w = wc
			continue
		}
		emit(table[w], fmt.Sprintf("w=%q not extended by %q", w, c))
		e := next
		table[wc] = e
		next++
		if e == 1<<width && width < maxWidth {
			width++
			events = append(events, naiveEvent{-1, width, fmt.Sprintf("entry %d: later codes use width %d", e, width)})
		}
		if e == tableSize-1 {
			events = append(events, naiveEvent{clearCode, width, fmt.Sprintf("entry %d: immediate clear at width %d", e, width)})
			reset()
			next, width = firstFree, minWidth
		}
		w = c
	}
	if w != "" {
		emit(table[w], "flush pending match at Close")
	}
	if next == 1<<width && width < maxWidth {
		width++
		phantomAtClose = true
		events = append(events, naiveEvent{-1, width, fmt.Sprintf("phantom number %d at Close: width %d", next, width)})
	}
	events = append(events, naiveEvent{endCode, width, "end-of-information"})

	acc := uint32(0)
	nbits := 0
	for _, ev := range events {
		if ev.code < 0 {
			continue
		}
		acc |= uint32(ev.code) << nbits
		nbits += ev.width
		for nbits >= 8 {
			packed = append(packed, byte(acc))
			acc >>= 8
			nbits -= 8
		}
	}
	if nbits > 0 {
		packed = append(packed, byte(acc))
	}
	return events, packed, phantomAtClose
}

// codeSequence drops width/note metadata for comparisons against the spec
// example.
func codeSequence(events []naiveEvent) []int {
	var out []int
	for _, ev := range events {
		if ev.code >= 0 {
			out = append(out, ev.code)
		}
	}
	return out
}
