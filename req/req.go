package req

import "errors"

var (
	ErrHeaderIncomplete   = errors.New("wal file header is incomplete")
	ErrBatchHeaderMissing = errors.New("batch header is incomplete")
	ErrEntryIncomplete    = errors.New("batch entry is incomplete")
	ErrCRC                = errors.New("batch crc mismatch")
	ErrClosed             = errors.New("group committer is closed")
)

type Request struct {
	ID      uint64
	Payload []byte
	done    chan Result
}

type Result struct {
	ID      uint64
	Seq     uint64
	Payload []byte
	Err     error
}

func New(id uint64, payload []byte) Request {
	return Request{
		ID:      id,
		Payload: payload,
		done:    make(chan Result, 1),
	}
}

func (r Request) Done() <-chan Result {
	return r.done
}

func (r Request) resolve(result Result) {
	if r.done != nil {
		r.done <- result
	}
}
