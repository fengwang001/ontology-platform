package ontology

import "strconv"

func (kind CommitKind) String() string {
	switch kind {
	case CommitRejected:
		return "rejected"
	case CommitOk:
		return "ok"
	case CommitConflict:
		return "conflict"
	case CommitWatermark:
		return "watermark"
	default:
		return "unknown-" + strconv.Itoa(int(kind))
	}
}

func (reason RejectReason) String() string {
	switch reason {
	case RejectNone:
		return "none"
	case RejectUnknownTransaction:
		return "unknown-transaction"
	case RejectTooManyKeys:
		return "too-many-keys"
	case RejectInvalidKey:
		return "invalid-key"
	default:
		return "unknown-" + strconv.Itoa(int(reason))
	}
}
