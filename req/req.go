package req

import "errors"

var (
	ErrClosed       = errors.New("group commit is closed")
	ErrHeaderShort  = errors.New("wal header is incomplete")
	ErrBatchHeader  = errors.New("wal batch header is incomplete")
	ErrEntryShort   = errors.New("wal entry is incomplete")
	ErrCRCMismatch  = errors.New("wal batch crc mismatch")
	ErrSyncFailed   = errors.New("wal sync failed")
	ErrWriteFailed  = errors.New("wal write failed")
)

type Request struct {
	Payload []byte
	done    chan struct{}
	seq     uint64
	err     error
}

func New(payload []byte) *Request {
	body := append([]byte(nil), payload...)
	return &Request{Payload: body, done: make(chan struct{})}

}

func (r *Request) Seq() uint64 {
	<-r.done
	return r.seq
}

func (r *Request) Err() error {
	<-r.done
	return r.err
}

func (r *Request) Done() <-chan struct{} {
	return r.done
}

func (r *Request) Complete(seq uint64, err error) {
	r.seq = seq
	r.err = err
	close(r.done)
}
