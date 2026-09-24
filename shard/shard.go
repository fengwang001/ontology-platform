// Package shard 定义分片查询接口、分片状态与可注入故障的假分片实现。
package shard

import (
	"context"
	"errors"
	"time"
)

// Record 是分片返回的一条记录。Value 用于 Count/Sum/Min/Max，Score 用于 TopK。
// 缺字段时对应 Go 零值（部分字段返回是合法的）。
type Record struct {
	ID    string
	Value float64
	Score float64
}

// Response 是一次分片查询的返回。
// Claimed 为分片自己声称的记录条数；与 len(Records) 不符即损坏。
// MaxScore/HasMax 给出本分片任意单条记录 Score 的上界（供 TopK 可信前缀使用）。
type Response struct {
	Records  []Record
	Claimed  int
	MaxScore float64
	HasMax   bool
}

// Status 是分片在一次扇出中的最终状态。
type Status int

const (
	StatusUnknown Status = iota
	StatusOK             // 成功且至少一条记录
	StatusEmpty          // 成功但无记录（与失败严格区分）
	StatusTimeout
	StatusCorrupt
	StatusFailed // 其他错误（含被取消）
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusEmpty:
		return "empty"
	case StatusTimeout:
		return "timeout"
	case StatusCorrupt:
		return "corrupt"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ErrCorrupt 表示分片返回数据与其声称条数不符。
var ErrCorrupt = errors.New("shard: corrupt response")

// Shard 是可注入的分片接口。
type Shard interface {
	ID() string
	Query(ctx context.Context) (Response, error)
}

// Option 配置假分片。
type Option func(*Fake)

// WithDelay 让分片在返回前等待 d（或 ctx 提前结束）。
func WithDelay(d time.Duration) Option {
	return func(f *Fake) { f.delay = d }
}

// WithHang 让分片永不主动返回，直到 ctx 结束（模拟超时）。
func WithHang() Option {
	return func(f *Fake) { f.hang = true }
}

// WithCorrupt 让分片声称的条数与实际不符。
func WithCorrupt() Option {
	return func(f *Fake) { f.corrupt = true }
}

// WithDuplicate 让分片把同一批记录返回两次（模拟重试叠加）。
func WithDuplicate() Option {
	return func(f *Fake) { f.duplicate = true }
}

// WithError 让分片返回自定义错误。
func WithError(err error) Option {
	return func(f *Fake) { f.fail = err }
}

// WithMax 声明本分片单条记录 Score 的上界。
func WithMax(m float64) Option {
	return func(f *Fake) { f.max = m; f.hasMax = true }
}

// Fake 是进程内模拟分片，不发生真实网络。
type Fake struct {
	id        string
	records   []Record
	delay     time.Duration
	hang      bool
	corrupt   bool
	duplicate bool
	fail      error
	max       float64
	hasMax    bool
}

// NewFake 构造假分片。id 允许为空串。
func NewFake(id string, records []Record, opts ...Option) *Fake {
	f := &Fake{id: id, records: records}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

func (f *Fake) ID() string { return f.id }

// Query 按配置返回数据、延迟、挂起或错误。
func (f *Fake) Query(ctx context.Context) (Response, error) {
	if f.hang {
		<-ctx.Done()
		return Response{}, ctx.Err()
	}
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return Response{}, ctx.Err()
		}
	}
	if f.fail != nil {
		return Response{}, f.fail
	}
	recs := make([]Record, len(f.records))
	copy(recs, f.records)
	if f.duplicate {
		recs = append(recs, recs...)
	}
	claimed := len(recs)
	if f.corrupt {
		claimed++ // 故意与实际条数不符
	}
	return Response{
		Records:  recs,
		Claimed:  claimed,
		MaxScore: f.max,
		HasMax:   f.hasMax,
	}, nil
}
