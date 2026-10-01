// Package fragmentlog implements a block-aligned, fragmenting record log.
//
// Records of arbitrary length are split into fragments that never cross a
// fixed-size block boundary. Every fragment carries a 7-byte header:
//
//	[0:4] little-endian crc32 (IEEE) of the type byte followed by the data
//	[4:6] little-endian uint16 data length
//	[6]   fragment type: 1=full, 2=first, 3=middle, 4=last
//
// If the space left in the current block is smaller than the header size it
// is padded with zero bytes and the fragment starts in the next block. A
// leftover of exactly 7 bytes with a non-empty record produces a
// zero-length "first" fragment; an empty record produces a zero-length
// "full" fragment.
//
// The Reader reports four distinguishable corruption classes (ErrLength,
// ErrChecksum, ErrSequence, ErrTruncated), judged in a fixed order, and
// resynchronises to the next block boundary after each error so that
// subsequent records can still be recovered.
package fragmentlog
