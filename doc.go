// Package ontology implements an order-preserving byte encoder.
//
// A row consists of several sort keys, each of which is an int64,
// float64, string or nil. Encode maps a row to a single byte string
// such that bytes.Compare on encoded rows agrees with key-by-key
// comparison of the original values. Decode reverses Encode exactly.
//
// Encoding layout (see README.md for the full specification):
//
//	nil / NaN        0x00                                   (always sorts first,
//	                                                       never direction-flipped)
//	negative number  0x01 + complemented(exp2 + mantissa) + typeByte
//	zero             0x02 + typeByte
//	positive number  0x03 + exp2 + mantissa + typeByte
//	string           0x04 + escaped bytes + 0x00 terminator
//
// Numbers are encoded as sign class, a 2-byte biased binary exponent
// and a trimmed big-endian fraction, so byte order equals numeric
// order across int64 and float64. The trailing typeByte (0x01 int64,
// 0x02 float64) breaks numeric ties (int64 sorts before float64) and
// preserves the original type through Decode. Descending keys are
// produced by complementing every byte of the key encoding except
// the nil tag.
package ontology
