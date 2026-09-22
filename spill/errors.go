package spill

import "errors"

// Definitive run-file corruption classes. Every failure to read a run
// file is classified as exactly one of these via errors.Is, which lets
// the recovery flow (and the byte-by-byte truncation tests) decide what
// prefix is salvageable.
var (
	// ErrHeaderIncomplete: fewer than HeaderSize bytes present.
	ErrHeaderIncomplete = errors.New("spill: header incomplete")
	// ErrHeaderCRC: header bytes present but its self-checksum mismatches.
	ErrHeaderCRC = errors.New("spill: header crc mismatch")
	// ErrBadMagic/Version: header complete but not a known run file.
	ErrBadMagic   = errors.New("spill: bad magic")
	ErrBadVersion = errors.New("spill: unsupported version")
	// ErrLengthPrefixIncomplete: a record frame starts but its 4-byte
	// length prefix is cut.
	ErrLengthPrefixIncomplete = errors.New("spill: length prefix incomplete")
	// ErrRecordIncomplete: the length prefix is whole but the record
	// body it announces (frame payload + CRC32) is cut.
	ErrRecordIncomplete = errors.New("spill: record body incomplete")
	// ErrCRC: a full frame is present but its CRC32 mismatches.
	ErrCRC = errors.New("spill: record crc mismatch")
	// ErrEmptyFile distinguishes a truly empty file (0 bytes) from a
	// valid zero-record run (header only).
	ErrEmptyFile = errors.New("spill: empty file (not a run)")
	// ErrTrailingData: the announced record count was read but bytes
	// remain (partial next record after a crash mid-append).
	ErrTrailingData = errors.New("spill: trailing bytes after record count")
)
