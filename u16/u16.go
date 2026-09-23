package u16

import "ontology/scalar"

type Order int

const (
	LE Order = iota
	BE
)

type Event struct {
	R       rune
	Size    int
	OK      bool
	Partial bool
}

func Next(p []byte, bo Order, eof bool) Event { return Event{} }

func Encode(r rune, bo Order) []byte { return nil }

var _ = scalar.MaxRune
