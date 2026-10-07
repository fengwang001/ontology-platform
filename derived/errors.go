package derived

// ErrorKind identifies a fixed error class of the derived-index subsystem.
type ErrorKind int

const (
	KindSourceNotFound ErrorKind = iota + 1
	KindUnsupportedLinkType
	KindNotUnique
	KindCycleDetected
	KindDownstreamUpdateFailed
)

// IndexError carries a classified error and the context that produced it.
type IndexError struct {
	Kind    ErrorKind
	Message string
}

func (e *IndexError) Error() string { return e.Message }

// kindPriority is the high-to-low reporting priority.
var kindPriority = [...]ErrorKind{
	KindSourceNotFound,
	KindUnsupportedLinkType,
	KindNotUnique,
	KindCycleDetected,
	KindDownstreamUpdateFailed,
}

// highestPriorityError returns the error with the highest fixed priority.
func highestPriorityError(errs []*IndexError) *IndexError {
	if len(errs) == 0 {
		return nil
	}
	rank := map[ErrorKind]int{}
	for i, k := range kindPriority {
		rank[k] = i
	}
	best := errs[0]
	for _, e := range errs[1:] {
		if rank[e.Kind] < rank[best.Kind] {
			best = e
		}
	}
	return best
}
