package ontology

// RejectReason 描述一次写请求被拒绝的可区分原因。
type RejectReason string

const (
	RejectInvalidRequest   RejectReason = "invalid_request"
	RejectFencedEpoch      RejectReason = "fenced_epoch"
	RejectFirstSeqNotZero  RejectReason = "first_seq_not_zero"
	RejectOutOfOrder       RejectReason = "out_of_order"
	RejectDuplicateExpired RejectReason = "duplicate_expired"
)

// Request 是生产者发起的一次写请求。
type Request struct {
	Producer  string
	Epoch     int64
	Partition string
	Seq       int64
	Payload   []byte
}

// Result 描述一次写请求的判定结果。
type Result struct {
	Accepted  bool
	Offset    int64
	Seq       int64
	Duplicate bool
	Basis     string
	Reason    RejectReason
}
