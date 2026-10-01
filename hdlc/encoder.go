package hdlc

import (
	"io"
	"sync"
)

const DefaultMaxPayload = 65535

var (
	ErrEmptyPayload   = hdlcError("hdlc: payload must not be empty")
	ErrPayloadTooLong = hdlcError("hdlc: payload exceeds maximum length")
)

type hdlcError string

func (e hdlcError) Error() string { return string(e) }

type Encoder struct {
	mu         sync.Mutex
	w          io.Writer
	maxPayload int

	pendingByte byte
	pendingBits uint8
	started     bool
}

func NewEncoder(w io.Writer, maxPayload int) *Encoder {
	if maxPayload <= 0 {
		maxPayload = DefaultMaxPayload
	}
	return &Encoder{w: w, maxPayload: maxPayload}
}

func (e *Encoder) WriteFrame(payload []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(payload) == 0 {
		return ErrEmptyPayload
	}
	if len(payload) > e.maxPayload {
		return ErrPayloadTooLong
	}

	if !e.started {
		if err := e.writeFlagLocked(); err != nil {
			return err
		}
		e.started = true
	}

	fcs := CRC16X25(payload)
	content := make([]byte, 0, len(payload)+2)
	content = append(content, payload...)
	content = append(content, byte(fcs), byte(fcs>>8))

	ones := 0
	writeContentBit := func(bit byte) error {
		if bit == 0 {
			if ones == 5 {
				if err := e.writeBitLocked(0); err != nil {
					return err
				}
				ones = 0
			}
			ones = 0
			return e.writeBitLocked(0)
		}
		if ones == 5 {
			if err := e.writeBitLocked(0); err != nil {
				return err
			}
			ones = 0
		}
		if err := e.writeBitLocked(1); err != nil {
			return err
		}
		ones++
		return nil
	}

	for _, contentByte := range content {
		for bitIndex := 0; bitIndex < 8; bitIndex++ {
			if err := writeContentBit((contentByte >> bitIndex) & 1); err != nil {
				return err
			}
		}
	}
	if ones == 5 {
		if err := e.writeBitLocked(0); err != nil {
			return err
		}
	}

	return e.writeFlagLocked()
}

func (e *Encoder) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	paddingBits := 8 - e.pendingBits
	for range paddingBits {
		if err := e.writeBitLocked(1); err != nil {
			return err
		}
	}
	e.started = false
	return nil
}

func (e *Encoder) writeFlagLocked() error {
	flagBits := []byte{0, 1, 1, 1, 1, 1, 1, 0}
	for _, bit := range flagBits {
		if err := e.writeBitLocked(bit); err != nil {
			return err
		}
	}
	return nil
}

func (e *Encoder) writeBitLocked(bit byte) error {
	if bit != 0 {
		e.pendingByte |= 1 << e.pendingBits
	}
	e.pendingBits++
	if e.pendingBits < 8 {
		return nil
	}

	if _, err := e.w.Write([]byte{e.pendingByte}); err != nil {
		return err
	}
	e.pendingByte = 0
	e.pendingBits = 0
	return nil
}
