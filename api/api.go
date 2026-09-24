// Package api is the public face of the causal buffer; it depends only
// on cbuf, which in turn depends on vc.
package api

import (
	"errors"

	"ontology/cbuf"
	"ontology/vc"
)

type Msg = vc.Msg

// Four distinguishable sentinel errors; all four are distinct values.
var (
	ErrBadParams  = errors.New("api: illegal parameters (n>0, maxBuf>=0 required)")
	ErrBadSender  = vc.ErrBadSender
	ErrBadVector  = vc.ErrBadVector
	ErrBufferFull = cbuf.ErrBufferFull
)

// Buffer is a receiver-only causal buffer, safe for concurrent use.
type Buffer struct{ in *cbuf.Buffer }

// New creates a buffer for n senders with buffer capacity maxBuf.
func New(n, maxBuf int) (*Buffer, error) {
	if n <= 0 || maxBuf < 0 {
		return nil, ErrBadParams
	}
	x, err := cbuf.New(n, maxBuf)
	if err != nil {
		return nil, ErrBadParams
	}
	return &Buffer{x}, nil
}

// Receive handles one arrival; it returns every message delivered,
// cascade included, in delivery order.
func (b *Buffer) Receive(m Msg) ([]Msg, error) {
	if e := vc.Legal(len(b.Local()), m); e != nil {
		return nil, e
	}
	return b.in.Receive(m)
}

func (b *Buffer) Delivered() []Msg { return b.in.Delivered() }
func (b *Buffer) Buffered() []Msg  { return b.in.Buffered() }
func (b *Buffer) Local() []int64   { return b.in.Local() }
func (b *Buffer) Dups() int64      { return b.in.Dups() }
