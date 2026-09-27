package u16

import "ontology/scalar"

type Order int

const (
	LittleEndian Order = iota
	BigEndian
)

type Event struct {
	R         scalar.Value
	Invalid   bool
	Start     int
	Len       int
	Truncated bool
	BOM       bool
	Checked   int
}

type Decoder struct {
	pending []byte
	order   Order
	offset  int
	checks  int
}

func NewDecoder(order Order) *Decoder {
	return &Decoder{order: order}
}

func (d *Decoder) Feed(p []byte, end bool, visit func(Event)) (int, int) {
	return 0, d.checks
}

func Encode(v scalar.Value, order Order) []byte {
	return nil
}
