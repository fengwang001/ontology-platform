// Package merger 实现分片扇出查询的部分结果合并器：
// 将同一聚合查询发往多个分片，在并发上限与截止时间内收集结果，
// 并在部分分片失败时给出带可信范围的答案。
package merger

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// AggKind 聚合类型。
type AggKind int

const (
	AggCount AggKind = iota // 计数
	AggSum                  // 求和
	AggMin                  // 最小值
	AggMax                  // 最大值
	AggTopK                 // 前 K 名
)

func (k AggKind) valid() bool { return k >= AggCount && k <= AggTopK }

func (k AggKind) String() string {
	switch k {
	case AggCount:
		return "count"
	case AggSum:
		return "sum"
	case AggMin:
		return "min"
	case AggMax:
		return "max"
	case AggTopK:
		return "topk"
	}
	return fmt.Sprintf("unknown(%d)", int(k))
}

// Aggregation 描述一次聚合查询；K 仅对 AggTopK 有意义。
type Aggregation struct {
	Kind AggKind
	K    int
}

// ShardInfo 分片登记信息，取值为非负整数上界。
type ShardInfo struct {
	Name       string
	RowBound   uint64 // 行数上界
	ValueBound uint64 // 取值上界
}

// RespKind 分片响应类别。
type RespKind int

const (
	RespOK      RespKind = iota // 正常返回
	RespError                   // 分片返回错误
	RespTimeout                 // 超时（截止时仍未响应）
)

// Result 分片返回的聚合结果。
type Result struct {
	Value    uint64   // Count/Sum/Min/Max 的值
	Values   []uint64 // TopK 的值列表
	HasValue bool     // Min/Max 是否有值（空分片为 false）
}

// ShardResponse 单个分片的一次响应。
type ShardResponse struct {
	Shard  string
	Kind   RespKind
	Result Result
}

// Answer 合并后的答案。区间语义随聚合种类而定：
//   - Count/Sum：真值 ∈ [Lower, Upper]
//   - Min：真值 ≤ Upper（Lower 为非负整数的平凡下界 0）
//   - Max：真值 ∈ [Lower, Upper]
//   - TopK：Items 为已收到数据合并出的前 K 名，前 CertainPrefix 项为确定
type Answer struct {
	Agg           Aggregation
	Complete      bool     // 全部分片成功，结果为精确值
	Inconclusive  bool     // 无分片成功（或 Min/Max 无任何可用值），无结论
	HasValue      bool     // Min/Max/TopK 是否存在任何值
	Lower         uint64   // 区间下界
	Upper         uint64   // 区间上界
	Items         []uint64 // TopK 合并结果（降序）
	CertainPrefix int      // TopK 中确定无误的前缀长度

	Succeeded  int      // 成功且通过上界校验的分片数
	Errors     int      // 返回错误的分片数
	Timeouts   int      // 超时的分片数
	Violations int      // 违反登记上界的分片数
	Duplicates int      // 重复返回被丢弃的响应数
	Missing    []string // 缺失分片名（排序后，确定性输出）

	PeakConcurrency int // 在途请求数峰值（由 Executor 填写）
}

// 可区分的整体拒绝原因，可用 errors.Is 判定。
var (
	ErrNoShards           = errors.New("merger: shard list is empty")
	ErrDuplicateShard     = errors.New("merger: duplicate shard name")
	ErrInvalidK           = errors.New("merger: K must be positive for top-k")
	ErrInvalidConcurrency = errors.New("merger: concurrency limit must be positive")
	ErrInvalidDeadline    = errors.New("merger: deadline must be positive")
	ErrUnknownAggregation = errors.New("merger: unknown aggregation")
)

// Validate 在发出任何请求前整体校验参数。
func Validate(shards []ShardInfo, agg Aggregation, concurrency int, deadline time.Duration) error {
	if len(shards) == 0 {
		return ErrNoShards
	}
	seen := make(map[string]struct{}, len(shards))
	for _, s := range shards {
		if _, dup := seen[s.Name]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateShard, s.Name)
		}
		seen[s.Name] = struct{}{}
	}
	if !agg.Kind.valid() {
		return fmt.Errorf("%w: kind=%d", ErrUnknownAggregation, int(agg.Kind))
	}
	if agg.Kind == AggTopK && agg.K <= 0 {
		return fmt.Errorf("%w: K=%d", ErrInvalidK, agg.K)
	}
	if concurrency <= 0 {
		return fmt.Errorf("%w: got %d", ErrInvalidConcurrency, concurrency)
	}
	if deadline <= 0 {
		return fmt.Errorf("%w: got %s", ErrInvalidDeadline, deadline)
	}
	return nil
}

// satAdd/satMul 为防溢出的饱和运算，保证上界估计永不回绕而夸大或漏报。
func satAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

func satMul(a, b uint64) uint64 {
	if a != 0 && b > math.MaxUint64/a {
		return math.MaxUint64
	}
	return a * b
}
