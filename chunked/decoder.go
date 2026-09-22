package chunked

import (
	"ontology/frame"
	"ontology/hexline"
)

// Write feeds the next fragment of the encoded stream and returns how
// many bytes were consumed. Fragments may be split at any byte.
//
// Limits are enforced the moment they are exceeded. On error the
// decoder enters a terminal state: later Writes return the same error
// and Body stays intact. Once the message completes, Write stops
// consuming (consumed may be less than len(p)) and further Writes
// return ErrClosed.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.done {
		return 0, ErrClosed
	}
	if d.err != nil {
		return 0, d.err
	}
	consumed := 0
	for consumed < len(p) && !d.done {
		var n int
		var err error
		if d.state == StateTrailer {
			n, err = d.writeTrailer(p[consumed:])
		} else {
			n, err = d.writeFrame(p[consumed:])
		}
		consumed += n
		if err != nil {
			d.offset += consumed
			d.err = &Error{Offset: d.offset - 1, State: d.state, Err: err}
			return consumed, d.err
		}
	}
	d.offset += consumed
	return consumed, nil
}

// Close signals end of stream. It returns nil if the message is
// complete, ErrHalfCRLF if input stopped between CR and LF, or
// ErrIncomplete (with the stopping State) otherwise. Close after a
// terminal error returns that error.
func (d *Decoder) Close() error {
	if d.done {
		return nil
	}
	if d.err != nil {
		return d.err
	}
	if d.frame.HalfCR() || d.state == StateTrailer && d.trailers.PendingCR() {
		d.err = &Error{Offset: d.offset, State: d.state, Err: ErrHalfCRLF}
		return d.err
	}
	d.err = &Error{Offset: d.offset, State: d.state, Err: ErrIncomplete}
	return d.err
}

// writeFrame feeds one chunk frame and updates the decoder state.
func (d *Decoder) writeFrame(p []byte) (int, error) {
	body, n, frameDone, err := d.frame.Write(p, d.body, d.checkSize)
	d.body = body
	if err != nil || !frameDone {
		d.syncState()
		return n, err
	}
	if size, _ := d.frame.Size(); size == 0 {
		d.state = StateTrailer
		d.trailers = hexline.NewScanner(d.cfg.MaxLineLen)
	} else {
		d.frame = frame.New(d.cfg.MaxLineLen)
		d.state = StateSizeLine
	}
	return n, nil
}

// writeTrailer consumes trailer lines until the terminating empty
// line completes the message.
func (d *Decoder) writeTrailer(p []byte) (int, error) {
	line, n, complete, err := d.trailers.Write(p)
	if err != nil || !complete {
		return n, err
	}
	if len(line) == 0 {
		d.done = true
		d.state = StateDone
		return n, nil
	}
	d.ntrailer++
	if d.ntrailer > d.cfg.MaxTrailers {
		return n, ErrTooManyTrailers
	}
	return n, nil
}

// checkSize enforces the per-chunk and total body limits as soon as a
// size line is parsed, before any of its data is consumed.
func (d *Decoder) checkSize(size uint64) error {
	if size > d.cfg.MaxChunkSize {
		return ErrChunkTooLarge
	}
	if uint64(len(d.body))+size > d.cfg.MaxBodySize {
		return ErrBodyTooLarge
	}
	return nil
}

// syncState mirrors the frame's fine-grained state onto the decoder.
func (d *Decoder) syncState() {
	switch d.frame.State() {
	case frame.StateSizeLine:
		d.state = StateSizeLine
	case frame.StateData:
		d.state = StateData
	default:
		d.state = StateAwaitCRLF
	}
}
