package wire

import "encoding/binary"

const (
	Magic       = "ONLZ"
	Version     = 1
	TagLiteral  = 0
	TagBackref  = 1
	TagFlush    = 2
	TagEnd      = 3
	MaxVarint64 = 10
)

const tagShift = 62

func AppendHeader(dst []byte, windowSize uint64) []byte {
	dst = append(dst, Magic...)
	dst = append(dst, Version)
	return binary.AppendUvarint(dst, windowSize)
}

func AppendTag(dst []byte, tag int, value uint64) []byte {
	return binary.AppendUvarint(dst, uint64(tag)<<tagShift|value)
}

func SplitTag(code uint64) (tag int, value uint64) {
	return int(code >> tagShift), code &^ (uint64(3) << tagShift)
}

func AppendUvarint(dst []byte, value uint64) []byte {
	return binary.AppendUvarint(dst, value)
}

func ReadUvarint(src []byte) (value uint64, n int) {
	return binary.Uvarint(src)
}
