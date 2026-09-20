package chunked

import "strconv"

// lastChunk is the terminating block of a chunked stream.
const lastChunk = "0\r\n\r\n"

// appendChunkHeader appends the lowercase, non-zero-padded hexadecimal
// length header for a chunk of size n, e.g. "1a\r\n".
func appendChunkHeader(dst []byte, n int) []byte {
	dst = strconv.AppendInt(dst, int64(n), 16)
	return append(dst, '\r', '\n')
}

// appendChunk appends one full encoded chunk ("<hex>\r\n<data>\r\n")
// for the given payload, which must be non-empty.
func appendChunk(dst, payload []byte) []byte {
	dst = appendChunkHeader(dst, len(payload))
	dst = append(dst, payload...)
	return append(dst, '\r', '\n')
}
