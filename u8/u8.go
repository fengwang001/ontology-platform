package u8

import "ontology/scalar"

type Event struct {
	R          scalar.Value
	Invalid    bool
	Start      int
	Len        int
	Truncated  bool
	Checked    int
}

type Decoder struct {
	pending []byte
	offset  int
	checks  int
}

func NewDecoder() *Decoder {
	return &Decoder{}
}

func (d *Decoder) Feed(p []byte, end bool, visit func(Event)) (int, int) {
	return 0, d.checks
}

func Encode(v scalar.Value) []byte {
	return nil
}
