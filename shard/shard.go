// Package shard 定义分片接口与可注入故障的假分片实现。
package shard

import (
	"context"
	"time"
)

// Record 是一条记录。ID 允许为空串；V 为参与 Sum/Min/Max/TopK 的非负数值。
type Record struct {
	ID string
	V  int64
}

// Response 是分片的一次响应。Claimed 为分片自己声称的条数。
type Response struct {
	Records []Record
	Claimed int
	// Upper 是该分片任何记录分数的上界，供 TopK 可信度推导使用。
	Upper int64
}

// Shard 是可注入的分片接口。
type Shard interface {
	ID() string
	Query(ctx context.Context) Response
}

// Config 描述假分片的行为。
type Config struct {
	IDString  string
	Records   []Record
	Upper     int64
	Delay     time.Duration // 正常返回前的延迟
	Hang      bool          // 永不返回，只监听 ctx
	Corrupt   bool          // 返回的实际条数与 Claimed 不符
	Duplicate bool          // 同一次 Query 模拟重试叠加：投递两份响应
}

// Deliver 是带到达标记的一次投递。
type Deliver struct {
	Resp Response
}

// Fake 是进程内模拟分片。
type Fake struct {
	cfg Config
}

// New 构造假分片。
func New(cfg Config) *Fake {
	return &Fake{cfg: cfg}
}

// ID 返回分片 ID（允许空串）。
func (f *Fake) ID() string { return f.cfg.IDString }

// Query 执行模拟查询，返回响应（Duplicate 时只返回一份；
// 重试叠加由 fanout 层的投递去重体现，见 fanout.Results 与 Expanded）。
func (f *Fake) Query(ctx context.Context) Response {
	if f.cfg.Hang {
		<-ctx.Done()
		return Response{}
	}
	if f.cfg.Delay > 0 {
		select {
		case <-time.After(f.cfg.Delay):
		case <-ctx.Done():
			return Response{}
		}
	}
	recs := make([]Record, len(f.cfg.Records))
	copy(recs, f.cfg.Records)
	claimed := len(recs)
	if f.cfg.Corrupt {
		claimed++ // 故意声称多一条，造成损坏
	}
	return Response{Records: recs, Claimed: claimed, Upper: f.cfg.Upper}
}

// Expanded 按配置把需要重复投递的分片展开为两份相同分片，
// 模拟"重试叠加"：扇出层会对同一分片 ID 收到两次成功响应。
func Expanded(shards []Shard) []Shard {
	var out []Shard
	for _, s := range shards {
		out = append(out, s)
		if f, ok := s.(*Fake); ok && f.cfg.Duplicate {
			out = append(out, New(f.cfg.withoutDup()))
		}
	}
	return out
}

func (c Config) withoutDup() Config {
	c.Duplicate = false
	return c
}
