// Package shard 定义分片接口、响应类型与可注入的假分片。
package shard

import (
	"context"
	"errors"
	"math"
)

// Record 是分片返回的一条记录。RecordID 允许为空串。
type Record struct {
	ID    string
	Score float64
}

// Response 是一次分片查询的响应。Count 是分片自己声称的条数。
type Response struct {
	ShardID string
	Count   int
	Records []Record
}

// Shard 是可注入的分片接口。ScoreBound 返回该分片可能贡献的最高分上界。
type Shard interface {
	ID() string
	ScoreBound() (bound float64, known bool)
	Query(ctx context.Context) ([]Response, error)
}

var (
	// ErrShardTimeout 表示分片在整体截止时间内未返回。
	ErrShardTimeout = errors.New("shard: query timeout")
	// ErrCorrupt 表示分片返回了损坏数据（条数不符/字段缺失）。
	ErrCorrupt = errors.New("shard: corrupt response")
)

// Validate 校验响应：声称条数必须与实际一致，且 Score 必须是有限数。
func Validate(r Response) error {
	if r.Count != len(r.Records) {
		return ErrCorrupt
	}
	for _, rec := range r.Records {
		if math.IsNaN(rec.Score) || math.IsInf(rec.Score, 0) {
			return ErrCorrupt
		}
	}
	return nil
}
