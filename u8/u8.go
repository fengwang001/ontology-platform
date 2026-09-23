package u8

import "ontology/scalar"

type Event struct {
	R       rune
	Size    int
	Invalid bool
}

type Decoder struct{}

func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) Feed(b byte) (Event, bool) { return Event{}, false }

func (d *Decoder) PendingLen() int { return 0 }

func Encode(r rune) []byte {
	_ = scalar.Valid
	return nil
}
