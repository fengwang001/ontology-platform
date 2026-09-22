package chunked

// Limits configures the decoder's rejection thresholds. Zero or negative
// values mean unlimited.
type Limits struct {
	// MaxSizeLine is the maximum byte length of one chunk-size line,
	// including its trailing CRLF.
	MaxSizeLine int
	// MaxChunk is the maximum number of bytes in one chunk's data.
	MaxChunk int64
	// MaxBody is the maximum total decoded message-body size.
	MaxBody int64
	// MaxTrailers is the maximum number of trailer lines accepted after
	// the zero-size chunk.
	MaxTrailers int
}

// DefaultLimits used by New: 8 KiB size lines, 1 MiB chunks, 16 MiB body,
// 64 trailer lines.
var DefaultLimits = Limits{
	MaxSizeLine: 8 * 1024,
	MaxChunk:    1 << 20,
	MaxBody:     16 << 20,
	MaxTrailers: 64,
}

// New returns a decoder using DefaultLimits.
func New() *Decoder {
	return NewWithLimits(DefaultLimits)
}

// NewWithLimits returns a decoder using the supplied limits.
func NewWithLimits(limits Limits) *Decoder {
	return &Decoder{limits: limits}
}
