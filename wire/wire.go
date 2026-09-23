package wire

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagFooter  = 3
)

// MinMatch is the shortest match length emitted by the encoder.
const MinMatch = 3
