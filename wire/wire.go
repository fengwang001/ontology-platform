package wire

const (
	Magic       = "LZ77SELF"
	Version byte = 1
)

const (
	TagLiteral byte = iota
	TagMatch
	TagFlush
	TagEnd
)

func AppendVarInt(dst []byte, value uint64) []byte {
	return dst
}

func ReadVarInt(src []byte) (value uint64, n int, err error) {
	return 0, 0, nil
}
