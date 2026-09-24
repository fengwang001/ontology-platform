package qpline

const MaxLineLength = 76

type Encoder struct {
	output []byte
}

func Encode(input []byte) []byte {
	encoder := &Encoder{}
	encoder.write(input)
	return encoder.finish()
}

func (e *Encoder) write(input []byte) {}

func (e *Encoder) finish() []byte {
	return e.output
}
