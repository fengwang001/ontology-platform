// Package pct performs byte-wise percent-encoding normalization and
// validation: escape hex digits are upper-cased, redundant escapes of bytes
// declared literal-safe are decoded, malformed escapes and invalid UTF-8 are
// rejected with their byte offset. It depends on no other project package.
package pct

// ByteScanner receives the number of input bytes consumed by a scan pass.
// Package clients use it to prove single-pass linear work.
type ByteScanner func(n int)
