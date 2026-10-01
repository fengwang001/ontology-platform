package quota

// Op is one operation inside a Batch. Fields are selected by Kind.
type Op struct {
	Kind  string
	X     ID
	P     ID
	Size  int64
	Bytes int64
	N     int64
}

const (
	OpMkdir    = "Mkdir"
	OpAddFile  = "AddFile"
	OpResize   = "Resize"
	OpRemove   = "Remove"
	OpRename   = "Rename"
	OpReserve  = "Reserve"
	OpRelease  = "Release"
	OpSetQuota = "SetQuota"
)

// BatchError reports the index of the first failing op and its reason.
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string   { return e.Err.Error() }
func (e *BatchError) Unwrap() []error { return []error{ErrBatch, e.Err} }

// Batch applies ops atomically: either all succeed or every change (usage,
// reserve, quota, ids) is rolled back. It returns the ids allocated by the
// Mkdir/AddFile ops, in order.
func (l *Ledger) Batch(ops []Op) (ids []ID, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(ops) == 0 {
		return nil, &EmptyBatchError{}
	}
	work := l.t.clone()
	allocated := make([]ID, 0, len(ops))
	for i, op := range ops {
		var id ID
		var perr error
		switch op.Kind {
		case OpMkdir:
			id, perr = work.mkdir(op.P)
		case OpAddFile:
			id, perr = work.addFile(op.P, op.Size)
		case OpResize:
			perr = work.resize(op.X, op.Size)
		case OpRemove:
			perr = work.remove(op.X)
		case OpRename:
			perr = work.rename(op.X, op.P)
		case OpReserve:
			perr = work.reserve(op.X, op.Bytes)
		case OpRelease:
			perr = work.release(op.X, op.Bytes)
		case OpSetQuota:
			perr = work.setQuota(op.X, op.Bytes, op.N)
		default:
			perr = ErrInvalid
		}
		if perr != nil {
			return nil, &BatchError{Index: i, Err: perr}
		}
		if op.Kind == OpMkdir || op.Kind == OpAddFile {
			allocated = append(allocated, id)
		}
	}
	l.t = work
	return allocated, nil
}
