package shard

import (
	"context"
	"time"
)

// Fake 是可注入的假分片。
type Fake struct {
	id       string
	records  []Record
	delay    time.Duration
	hang     bool
	corrupt  bool
	bound    float64
	hasBound bool
}

// Config 描述一个假分片。
type Config struct {
	ID       string
	Records  []Record
	Delay    time.Duration
	Hang     bool        // 永不返回，直到 ctx 取消
	Corrupt  bool        // 返回条数与声称条数不符
	Bound    float64     // 单条得分上界
	HasBound bool        // 是否提供上界
}

// NewFake 按 Config 构造假分片。
func NewFake(c Config) *Fake {
	rec := make([]Record, len(c.Records))
	copy(rec, c.Records)
	return &Fake{
		id:       c.ID,
		records:  rec,
		delay:    c.Delay,
		hang:     c.Hang,
		corrupt:  c.Corrupt,
		bound:    c.Bound,
		hasBound: c.HasBound,
	}
}

func (f *Fake) ID() string { return f.id }

func (f *Fake) resp() Response {
	claimed := len(f.records)
	recs := f.records
	if f.corrupt {
		claimed = len(f.records) + 3 // 声称条数与实际不符
	}
	return Response{
		ShardID:      f.id,
		ClaimedCount: claimed,
		Records:      recs,
		ScoreBound:   f.bound,
		HasBound:     f.hasBound,
	}
}

// Fetch 模拟一次分片请求。
func (f *Fake) Fetch(ctx context.Context) (Response, error) {
	if f.hang {
		<-ctx.Done()
		return Response{}, ErrTimeout
	}
	if f.delay > 0 {
		t := time.NewTimer(f.delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return Response{}, ErrTimeout
		case <-t.C:
		}
	}
	return f.resp(), nil
}

// Duplicating 包装一个分片，单次 Fetch 模拟重试叠加，经 deliveries 通道
// 交付两次应答；用于验证扇出层按 ShardID 去重、不重复计数。
type Duplicating struct {
	inner *Fake
}

// NewDuplicating 包装 f，使其在 MultiFetch 中交付两次。
func NewDuplicating(f *Fake) *Duplicating { return &Duplicating{inner: f} }

func (d *Duplicating) ID() string { return d.inner.ID() }

func (d *Duplicating) Fetch(ctx context.Context) (Response, error) {
	return d.inner.Fetch(ctx)
}

// Responses 交付两次相同应答（第二次为重试叠加）。
func (d *Duplicating) Responses() []Response {
	r := d.inner.resp()
	return []Response{r, r}
}
