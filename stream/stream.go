package stream

import (
	"ontology/u16"
	"ontology/u8"
)

type Format uint8

const (
	UTF8 Format = iota
	UTF16LE
	UTF16BE
)

type Mode uint8

const (
	Replace Mode = iota
	Strict
)

type Config struct {
	From      Format
	To        Format
	Mode      Mode
	KeepBOM   bool
	MaxOutput int
}

type Transcoder struct {
	cfg     Config
	out     []byte
	pending []byte
	u8d     u8.Decoder
	u16d    u16.Decoder
}

func New(cfg Config) *Transcoder {
	return &Transcoder{cfg: cfg}
}
