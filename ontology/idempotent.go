// Package ontology 提供幂等生产者（Idempotent Producer）的服务端序号校验。
//
// 服务端按 (生产者, 世代, 分区, 序号) 四元组判定写请求：
// 重复请求不重复落盘但仍确认成功（返回原分配位点），跳号被拒，
// 过期世代被围栏，世代升级后序号重新从 0 开始。
package ontology

import (
	"context"
	"log/slog"
)

// RejectReason 是请求被整体拒绝的可区分原因类别。
type RejectReason string

const (
	// ReasonInvalid 请求字段非法（空生产者、空分区、世代或序号为负）。
	ReasonInvalid RejectReason = "invalid_request"
	// ReasonFenced 请求世代早于服务端记录的当前世代（过期世代围栏）。
	ReasonFenced RejectReason = "fenced_generation"
	// ReasonGap 新世代/新分区首条非零，或序号大于 lastSeq+1（跳号）。
	ReasonGap RejectReason = "sequence_gap"
	// ReasonOutOfOrder 序号小于当前窗口下界（乱序，既不追加也不重复）。
	ReasonOutOfOrder RejectReason = "out_of_order"
	// ReasonDuplicateExpired 序号落在最后序号之前但已滑出最近窗口，
// 无法确认是否为重复（重复确认已过期）。
	ReasonDuplicateExpired RejectReason = "duplicate_expired"
)

// RejectError 描述一次被整体拒绝的写请求。拒绝不改变任何服务端状态。
type RejectError struct {
	// Reason 是互不相同、可区分的拒绝原因。
	Reason RejectReason
	// Detail 是面向日志的人类可读说明。
	Detail string
}

func (e *RejectError) Error() string {
	return string(e.Reason) + ": " + e.Detail
}

// WriteRequest 是生产者发起的一条写请求。
type WriteRequest struct {
	// ProducerID 生产者标识，不可为空。
	ProducerID string
	// Generation 生产者当前世代（epoch/fencing token），必须非负。
	Generation int64
	// Partition 目标分区，不可为空。
	Partition string
	// Sequence 该世代、该分区内的从 0 开始连续序号。
	Sequence int64
	// Payload 业务载荷，可为任意字节。
	Payload []byte
}

// Record 是一条被接受并落盘的记录。
type Record struct {
	ProducerID string
	Generation int64
	Partition  string
	Sequence   int64
	Offset     int64
	Payload    []byte
}

// AppendResult 是一次写请求的判定结果。
type AppendResult struct {
	// Duplicate 为 true 表示该请求是窗口内重复：没有重复落盘，
	// Offset 指向第一次接受时分配的同一位点。
	Duplicate bool
	// Offset 是该（世代, 分区, 序号）对应的全局日志位点。
	Offset int64
}

// Server 是幂等生产者序号校验服务端。
// 所有方法对多个执行体并发调用都是安全的。
type Server struct {
	logger *slog.Logger
}

// Option 配置 Server。
type Option func(*Server)

// WithLogger 设置判定日志使用的 logger；默认为 slog.Default()。
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) { s.logger = logger }
}

// NewServer 创建一个空的序号校验服务端。
func NewServer(opts ...Option) *Server {
	s := &Server{logger: slog.Default()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Append 按 (生产者, 世代, 分区, 序号) 校验并落盘一条写请求。
//
// 接受（追加或重复确认）返回 *AppendResult；拒绝返回 *RejectError。
// 重复确认与任何拒绝都不改变日志、当前世代、序号状态与窗口。
// 同一序号在同一分区至多落盘一次，接受后的序号按位点连续。
// 结果是确定的：同样的请求重放得到同样的判定与位点。
func (s *Server) Append(ctx context.Context, req WriteRequest) (*AppendResult, error) {
	_ = ctx
	return nil, &RejectError{Reason: ReasonInvalid, Detail: "not implemented"}
}

// CurrentGeneration 返回生产者当前世代；未知生产者返回 0。
func (s *Server) CurrentGeneration(producerID string) int64 { return 0 }

// LastSequence 返回生产者在给定分区、当前世代下的最后序号；
// 未知生产者或未知分区返回 -1。
func (s *Server) LastSequence(producerID, partition string) int64 { return -1 }

// Log 返回按接受顺序排列的已落盘记录副本。
func (s *Server) Log() []Record { return nil }
