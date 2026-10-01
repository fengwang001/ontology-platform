// Package hdlc implements an LSB-first, bit-stuffed HDLC frame encoder and
// streaming decoder.
//
// Frames contain a payload followed by a two-byte CRC-16/X-25 FCS. The
// physical stream uses 0x7E flags, inserts a zero after every five content
// ones, and treats seven consecutive physical ones as an abort.
package hdlc
