package req

type Request struct {
	Payload []byte
	token   uint64
}

type Result struct {
	Seq uint64
	Err error
}

type Entry struct {
	Seq     uint64
	Payload []byte
}

type Batch struct {
	FirstSeq uint64
	Entries  []Entry
	Items    []Request
}

func (b Batch) LastSeq() uint64 {
	if len(b.Entries) == 0 {
		return 0
	}
	return b.FirstSeq + uint64(len(b.Entries)) - 1
}

func (b Batch) PayloadBytes() int {
	total := 0
	for _, entry := range b.Entries {
		total += len(entry.Payload)
	}
	return total
}

func New(payload []byte, token uint64) Request {
	return Request{Payload: payload, token: token}
}

func (r Request) Token() uint64 {
	return r.token
}
