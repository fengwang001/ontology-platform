// Package shard 定义分片接口、可注入故障的假分片与响应校验。
package shard

import (
	"context"
	"errors"
	"time"
)

var (
	ErrCorrupt   = errors.New("shard: corrupt payload")
	ErrTimeout   = errors.New("shard: timeout")
	ErrDuplicate = errors.New("shard: duplicate delivery")
)

// Status 是单个分片在一次扇出中的最终状态。
type Status int

const (
	StatusUnknown Status = iota
	StatusOK
	StatusTimeout
	StatusCorrupt
	StatusDuplicate
	StatusFailed
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTimeout:
		return "timeout"
	case StatusCorrupt:
		return "corrupt"
	case StatusDuplicate:
		return "duplicate"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Record 是分片返回的一条本地聚合记录。零值字段配合 Has* 标志表示缺席。
type Record struct {
	ID       string
	Count    int64
	Sum      float64
	HasSum   bool
	Min      float64
	HasMin   bool
	Max      float64
	HasMax   bool
	Score    float64
	HasScore bool
}

// Partial 是一个分片的本地部分聚合。Bound 为其 TopK 条目得分上界。
type Partial struct {
	ID      string
	Claimed int
	Records []Record
	Bound   float64
}

// Shard 模拟一个可远程调用的分片。emit 可被调用多次以模拟重试叠加。
type Shard interface {
	ID() string
	Fetch(ctx context.Context, emit func(Partial)) error
}

// Validate 校验分片载荷：实际记录数必须与声称条数一致。
func Validate(p Partial) error {
	if len(p.Records) != p.Claimed {
		return ErrCorrupt
	}
	return nil
}

// Config 描述假分片的行为。
type Config struct {
	ID        string
	Delay     time.Duration
	Hang      bool // 永不主动返回，直到 ctx 取消
	Corrupt   bool // 声称条数与实际不符
	Duplicate bool // 同一载荷投递两次
	Fail      bool // 返回普通错误
	Partial   Partial
}

type fakeShard struct{ cfg Config }

// NewFake 按 cfg 构造一个假分片。
func NewFake(cfg Config) Shard { return fakeShard{cfg: cfg} }

func (f fakeShard) ID() string { return f.cfg.ID }

func (f fakeShard) Fetch(ctx context.Context, emit func(Partial)) error {
	if f.cfg.Hang {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.cfg.Delay > 0 {
		t := time.NewTimer(f.cfg.Delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	if f.cfg.Fail {
		return errors.New("shard: injected failure")
	}
	p := f.cfg.Partial
	p.ID = f.cfg.ID
	if f.cfg.Corrupt {
		p.Claimed = len(p.Records) + 1
	} else {
		p.Claimed = len(p.Records)
	}
	emit(p)
	if f.cfg.Duplicate {
		emit(p)
	}
	return nil
}
