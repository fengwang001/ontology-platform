package loader

// RequestInput carries the caller-supplied attributes of a new request.
type RequestInput struct {
	Origin      Origin
	URL         string
	Type        ResourceType
	As          ResourceType
	Priority    Priority
	Credentials CredentialsMode
	Integrity   string
	Size        int64
}

// Request is the internal mutable state of one registered request.
type Request struct {
	id        uint64
	origin    Origin
	url       string
	typ       ResourceType
	as        ResourceType
	prio      Priority
	cred      CredentialsMode
	integrity string
	size      int64
	progress  int64

	seq      uint64
	startSeq uint64
	pauses   int
	state    State

	fromCache bool
	hasErr    bool
	errKind   ErrorKind

	queued    bool
	attachTo  *Request
	attachers []*Request
}

func (r *Request) key() cacheKey { return cacheKey{origin: r.origin, url: r.url} }

func (r *Request) snapshot() Snapshot {
	snap := Snapshot{
		ID:        r.id,
		State:     r.state,
		Priority:  r.prio,
		Progress:  r.progress,
		Size:      r.size,
		Pauses:    r.pauses,
		FromCache: r.fromCache,
		HasErr:    r.hasErr,
		ErrKind:   r.errKind,
	}
	if r.attachTo != nil {
		snap.AttachTo = r.attachTo.id
	}
	return snap
}
