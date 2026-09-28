package ontology

import "context"

// Decision 表示服务端对一次写请求的判定类别。
type Decision string

const (
	DecisionInvalid         Decision = "invalid_request"
	DecisionStaleEpoch      Decision = "stale_epoch"
	DecisionGap             Decision = "sequence_gap"
	DecisionDuplicateStale  Decision = "duplicate_expired"
	DecisionOutOfOrder      Decision = "out_of_order"
	DecisionAcceptedAppend  Decision = "accepted_append"
	DecisionDuplicateAck    Decision = "duplicate_ack"
)

// WriteRequest 是生产者发往服务端的写请求。
type WriteRequest struct {
	Producer string
	Epoch    int64
	Partition string
	Sequence int64
	Payload  []byte
}

// LogEntry 是日志中一条已落盘记录。
type LogEntry struct {
	Offset    int64
	Producer  string
	Epoch     int64
	Partition string
	Sequence  int64
	Payload   []byte
}

// Result 是一次写请求的判定结果。
type Result struct {
	Decision Decision
	Epoch    int64
	Offset   int64
	LastSeq  int64
	Reason   string
}

// RejectError 表示一次被整体拒绝的写请求。
type RejectError struct {
	Decision Decision
	Reason   string
}

func (e *RejectError) Error() string { return string(e.Decision) + ": " + e.Reason }

// Server 是幂等生产者序号校验服务端。
type Server struct{}

// Option 配置 Server。
type Option func(*config)

type config struct{}

// NewServer 创建一个序号校验服务端。
func NewServer(opts ...Option) *Server {
	_ = opts
	return &Server{}
}

// Produce 判定并（在接受时）落盘一条写请求。
func (s *Server) Produce(ctx context.Context, req WriteRequest) (Result, error) {
	_ = ctx
	return Result{}, nil
}

// Log 返回日志快照。
func (s *Server) Log() []LogEntry { return nil }

// Snapshot 返回某生产者某分区的当前状态快照。
type Snapshot struct {
	Known   bool
	Epoch   int64
	LastSeq int64
	Window  []int64
}

// PartitionSnapshot 查询分区状态快照。
func (s *Server) PartitionSnapshot(producer, partition string) Snapshot {
	return Snapshot{}
}

// ProducerEpoch 查询生产者当前世代。
func (s *Server) ProducerEpoch(producer string) (int64, bool) {
	return 0, false
}
