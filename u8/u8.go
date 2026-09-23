package u8

import "ontology/scalar"

type Event struct {
	R       rune
	Size    int
	OK      bool
	Partial bool
}

func Next(p []byte, eof bool) Event { return Event{} }

func Encode(r rune) []byte { return nil }

var _ = scalar.MaxRune
