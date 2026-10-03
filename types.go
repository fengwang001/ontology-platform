package ontology

import "bytes"

type ReceiptKind string

const (
	Sent      ReceiptKind = "sent"
	Delivered ReceiptKind = "delivered"
	Read      ReceiptKind = "read"
	Soft      ReceiptKind = "soft"
	Hard      ReceiptKind = "hard"
)

type ReceiptResult string

const (
	Applied   ReceiptResult = "Applied"
	Stale     ReceiptResult = "Stale"
	Duplicate ReceiptResult = "Duplicate"
	Ignored   ReceiptResult = "Ignored"
)

type Failure string

const (
	NoFailure Failure = ""
	SoftFail  Failure = "soft"
	HardFail  Failure = "hard"
	Expired   Failure = "exp"
)

type AggregateStatus string

const (
	Failed          AggregateStatus = "failed"
	Inflight        AggregateStatus = "inflight"
	Partial         AggregateStatus = "partial"
	ReadStatus      AggregateStatus = "read"
	DeliveredStatus AggregateStatus = "delivered"
)

type RecipientStatus struct {
	Recipient []byte
	R         int
	Fail      Failure
	Late      bool
}

type MessageStatus struct {
	Message    []byte
	Status     AggregateStatus
	Recipients []RecipientStatus
}

type Expiration struct {
	Message   []byte
	Recipient []byte
}

type recipient struct {
	id      []byte
	r       int
	fail    Failure
	sentA   int
	softSet map[int]struct{}
	seen    map[receiptID]struct{}
	late    bool
}

type message struct {
	id         []byte
	deadline   int64
	order      []*recipient
	recipients map[string]*recipient
}

type receiptID struct {
	kind    ReceiptKind
	attempt int
}

func cloneBytes(v []byte) []byte {
	return bytes.Clone(v)
}
