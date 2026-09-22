package shard

type Record struct {
	ID        string
	Value     float64
	HasValue  bool
}

type Response struct {
	ShardID      string
	ClaimedCount int
	Records      []Record
	ScoreBound   float64
	HasBound     bool
}

type Shard interface {
	ID() string
	Fetch(ctx ContextLike) (Response, error)
}

type ContextLike interface {
	Done() <-chan struct{}
	Err() error
}
package shard

import (
	"context"
	"errors"
)

// Record 是分片返回的一条记录。HasValue=false 表示只给了部分字段，
// 该记录参与 Count，但不参与 Sum/Min/Max/TopK。
type Record struct {
	ID       string
	Value    float64
	HasValue bool
}

// Response 是一个分片的一次应答。len(Records) 必须等于 ClaimedCount，
// 否则视为损坏数据；ScoreBound 是该分片单条得分的上界（用于 TopK 推导）。
type Response struct {
	ShardID      string
	ClaimedCount int
	Records      []Record
	ScoreBound   float64
	HasBound     bool
}

// Shard 是可注入的分片接口（无真实网络）。
type Shard interface {
	ID() string
	Fetch(ctx context.Context) (Response, error)
}

// Status 是报告中每个分片的状态。
type Status int

const (
	StatusOK Status = iota
	StatusTimeout
	StatusCorrupt
	StatusMissing // 截止时未在飞且未应答，或分片缺失
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTimeout:
		return "timeout"
	case StatusCorrupt:
		return "corrupt"
	default:
		return "missing"
	}
}

var (
	// ErrTimeout 表示分片在截止时间内未返回或被 context 取消。
	ErrTimeout = errors.New("shard: timeout")
	// ErrCorrupt 表示分片返回的记录数与其声称的条数不符。
	ErrCorrupt = errors.New("shard: corrupt response")
	// ErrAllFailed 表示所有分片都失败，无任何成功数据可合并。
	ErrAllFailed = errors.New("shard: all shards failed")
	// ErrNoShards 表示查询没有配置任何分片。
	ErrNoShards = errors.New("shard: no shards configured")
)

// Valid 判断应答是否自洽：实际记录数必须等于声称条数。
func (r Response) Valid() bool { return len(r.Records) == r.ClaimedCount }
