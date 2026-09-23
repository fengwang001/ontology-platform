package segment

import "encoding/binary"

const (
	magic      = "ASEG"
	version    = 1
	headerSize = 24
)

// Header 是段文件的自描述头。
type Header struct {
	FirstSeq uint64 // 本段首条事件的序号
	Count    uint64 // 本段声称的事件数
}

func encodeHeader(h Header) []byte {
	b := make([]byte, headerSize)
	copy(b[0:4], magic)
	b[4] = version
	binary.BigEndian.PutUint64(b[5:13], h.FirstSeq)
	binary.BigEndian.PutUint64(b[13:21], h.Count)
	return b
}

// decodeHeader 解析恰好 headerSize 字节的段头。
func decodeHeader(b []byte) (Header, error) {
	if string(b[0:4]) != magic {
		return Header{}, ErrBadMagic
	}
	if b[4] != version {
		return Header{}, ErrBadMagic
	}
	return Header{
		FirstSeq: binary.BigEndian.Uint64(b[5:13]),
		Count:    binary.BigEndian.Uint64(b[13:21]),
	}, nil
}
