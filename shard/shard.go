// Package shard 定义分片查询接口与可注入故障的假分片。
package shard

import (
	"context"
	"errors"
	"time"
)

// Record 是一条记录。部分字段缺失时对应零值，仍为合法记录。
type Record struct {
	ID    string
	Score int64
}

// Status 是单个分片的终态枚举，四种故障可用它区分。
type Status int

const (
	StatusUnknown Status = iota
	StatusOK
	StatusTimedOut
	StatusCorrupt
	StatusCanceled
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTimedOut:
		return "timed_out"
	case StatusCorrupt:
		return "corrupt"
	case StatusCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

var (
	// ErrTimeout 分片自身超时（永久挂起由截止时间取消）。
	ErrTimeout = errors.New("shard: timeout")
	// ErrCorrupt 声称条数与实际记录数不符。
	ErrCorrupt = errors.New("shard: corrupt payload")
)

// Response 是一次分片查询的返回。Duplicate 为 true 表示这是同一分片的重复返回。
type Response struct {
	ID         string
	Claimed    int
	Records    []Record
	UpperBound int64 // -1 表示该分片不提供上界
	Err        error
	Duplicate  bool
	extra      []Response
}

// Extras 返回随同一分片叠加到达的重复响应（模拟重试叠加）。
func (r Response) Extras() []Response { return r.extra }

// Shard 是可注入的分片接口。
type Shard interface {
	ID() string
	Query(ctx context.Context) Response
}

// Config 配置假分片：延迟、超时(永久挂起)、损坏、重复返回。
type Config struct {
	ShardID    string
	Delay      time.Duration
	Timeout    bool
	Corrupt    bool
	Duplicate  bool
	Records    []Record
	Claimed    int   // 0 时自动取 len(Records)；显式损坏给与记录数不符的正值
	UpperBound int64 // -1 表示无上界
}

// New 按配置构造假分片。
func New(c Config) Shard {
	claimed := c.Claimed
	if claimed == 0 {
		claimed = len(c.Records)
	}
	if c.UpperBound == 0 {
		c.UpperBound = -1
	}
	if claimed != len(c.Records) {
		c.Corrupt = true
	}
	return &fake{cfg: c, claimed: claimed}
}

type fake struct {
	cfg     Config
	claimed int
}

func (f *fake) ID() string { return f.cfg.ShardID }

func (f *fake) Query(ctx context.Context) Response {
	if f.cfg.Timeout {
		<-ctx.Done() // 永不主动返回，等待整体截止取消
		return Response{ID: f.cfg.ShardID, Err: ErrTimeout}
	}
	if f.cfg.Delay > 0 {
		t := time.NewTimer(f.cfg.Delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return Response{ID: f.cfg.ShardID, Err: context.Cause(ctx)}
		case <-t.C:
		}
	}
	base := func() Response {
		if f.cfg.Corrupt {
			return Response{ID: f.cfg.ShardID, Claimed: f.claimed, Records: f.cfg.Records,
				UpperBound: f.cfg.UpperBound, Err: ErrCorrupt}
		}
		return Response{ID: f.cfg.ShardID, Claimed: f.claimed, Records: f.cfg.Records,
			UpperBound: f.cfg.UpperBound}
	}
	first := base()
	if f.cfg.Duplicate {
		dup := base()
		dup.Duplicate = true
		first.extra = []Response{dup}
	}
	return first
}
