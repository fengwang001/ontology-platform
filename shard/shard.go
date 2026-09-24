// Package shard 定义分片接口与可注入故障的假分片实现。
package shard

import (
	"context"
	"time"
)

// Record 是一条聚合记录：ID 为条目键，Value 为非负分数。
type Record struct {
	ID    string
	Value float64
}

// Response 是分片一次查询的返回。Claimed 是分片自报的条数，
// 与 len(Records) 不一致时视为数据损坏。
type Response struct {
	ShardID string
	Records []Record
	Claimed int
}

// Shard 是可注入的分片接口。Bound 返回本分片对任意单一 ID
// 可能贡献的分数上界，用于 TopK 可信度判定。
type Shard interface {
	ID() string
	Bound() float64
	Query(ctx context.Context) (Response, error)
}

// Fake 是可配置故障的假分片：延迟、挂起（永不返回）、损坏。
type Fake struct {
	id      string
	bound   float64
	records []Record
	delay   time.Duration
	hang    bool
	corrupt bool
}

// Option 配置 Fake 的故障行为。
type Option func(*Fake)

// WithDelay 让分片延迟 d 后返回；ctx 取消会打断等待。
func WithDelay(d time.Duration) Option {
	return func(f *Fake) { f.delay = d }
}

// WithHang 让分片永不返回，直到 ctx 取消。
func WithHang() Option {
	return func(f *Fake) { f.hang = true }
}

// WithCorrupt 让分片自报条数与实际条数不符。
func WithCorrupt() Option {
	return func(f *Fake) { f.corrupt = true }
}

// NewFake 构造一个假分片。id 允许为空串（合法）。
func NewFake(id string, bound float64, records []Record, opts ...Option) *Fake {
	f := &Fake{id: id, bound: bound, records: records}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// ID 返回分片标识。
func (f *Fake) ID() string { return f.id }

// Bound 返回分片对单一 ID 的分数上界。
func (f *Fake) Bound() float64 { return f.bound }

// Query 模拟一次分片查询，按配置注入延迟、挂起或损坏。
func (f *Fake) Query(ctx context.Context) (Response, error) {
	if f.hang {
		<-ctx.Done()
		return Response{}, ctx.Err()
	}
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-timer.C:
		}
	}
	claimed := len(f.records)
	if f.corrupt {
		claimed++
	}
	records := make([]Record, len(f.records))
	copy(records, f.records)
	return Response{ShardID: f.id, Records: records, Claimed: claimed}, nil
}
