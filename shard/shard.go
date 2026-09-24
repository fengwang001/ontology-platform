package shard

import (
	"context"
	"errors"
	"time"
)

var (
	ErrTimeout = errors.New("shard timeout")
	ErrCorrupt = errors.New("corrupt shard response")
)

type Status int

const (
	StatusUnknown Status = iota
	StatusOK
	StatusTimeout
	StatusCorrupt
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
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type Record struct {
	ID     string
	Value  int64
	Has    bool
	Score  int64
	HasTop bool
}

type Response struct {
	Records    []Record
	Declared   int
	UpperBound int64
}

type Shard interface {
	ID() string
	UpperBound() int64
	Query(context.Context) ([]Response, error)
}

type Attempt struct {
	ShardID  string
	Response Response
	Status   Status
	Err      error
	Launched bool
	Frames   int
}

type Fake struct {
	id         string
	records    []Record
	upper      int64
	delay      time.Duration
	corrupt    int
	hang       bool
	duplicate  bool
	duplicated bool
}

func NewFake(id string, records []Record, upper int64) *Fake {
	copied := append([]Record(nil), records...)
	return &Fake{id: id, records: copied, upper: upper}
}

func (f *Fake) ID() string { return f.id }
func (f *Fake) UpperBound() int64 { return f.upper }

func (f *Fake) WithDelay(d time.Duration) *Fake { f.delay = d; return f }
func (f *Fake) WithCorrupt(delta int) *Fake     { f.corrupt = delta; return f }
func (f *Fake) WithHang() *Fake                 { f.hang = true; return f }
func (f *Fake) WithDuplicate() *Fake            { f.duplicate = true; return f }

func (f *Fake) Query(ctx context.Context) ([]Response, error) {
	if f.hang {
		<-ctx.Done()
		return nil, ErrTimeout
	}
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ErrTimeout
		case <-timer.C:
		}
	}
	if f.duplicate && !f.duplicated {
		f.duplicated = true
		resp := f.response()
		return []Response{resp, resp}, nil
	}
	return []Response{f.response()}, nil
}

func (f *Fake) response() Response {
	records := append([]Record(nil), f.records...)
	resp := Response{Records: records, Declared: len(records), UpperBound: f.upper}
	if f.corrupt != 0 {
		resp.Declared += f.corrupt
	}
	return resp
}
