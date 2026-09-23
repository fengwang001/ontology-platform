package u16

import "ontology/scalar"

const (
	LE = 0
	BE = 1
)

const (
	Invalid = -1
	Pending = -2
)

type Decoder struct{}

func NewDecoder(order int) *Decoder { return &Decoder{} }

func (d *Decoder) Feed(b byte) (rune, int) { return 0, 0 }

func (d *Decoder) Truncated() bool { return false }

func (d *Decoder) Reset() {}

func (d *Decoder) PendingLen() int { return 0 }

func Encode(r rune, order int) []byte { return nil }

var _ = scalar.Valid
