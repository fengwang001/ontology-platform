package u8

import "ontology/scalar"

const (
	Invalid = -1
	Pending = -2
)

type Decoder struct{}

func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) Feed(b byte) (rune, int) { return 0, 0 }

func (d *Decoder) Truncated() bool { return false }

func (d *Decoder) Reset() {}

func (d *Decoder) PendingLen() int { return 0 }

func Encode(r rune) []byte { return nil }

func Boundary(in []byte, cut int) int { return cut }

var _ = scalar.Valid
