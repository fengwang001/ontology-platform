// Package journal 是 SAGA 的执行日志：按实例顺序追加「步骤/方向/结果」记录，
// 并支持按实例读回。时间戳由调用方（注入的时钟）提供，本包不接触时间 API。
package journal

import (
	"errors"
	"sync"
)

// Direction 是记录对应的执行方向。
type Direction int

const (
	Forward Direction = iota
	Compensate
)

func (d Direction) String() string {
	if d == Compensate {
		return "compensate"
	}
	return "forward"
}

// Result 是一次动作的落盘结果。
type Result int

const (
	Success Result = iota
	Failure
)

func (r Result) String() string {
	if r == Failure {
		return "failure"
	}
	return "success"
}

// Record 是一条不可变的执行日志记录。Seq 在整个 Journal 内全局递增（从 1 起）。
type Record struct {
	Seq        int64
	InstanceID string
	StepIndex  int
	StepKey    string
	Direction  Direction
	Result     Result
	Unknown    bool   // 正向未知结果按推定成功落盘时为 true
	At         int64  // 注入时钟给出的毫秒时间戳
	Detail     string // 错误信息（成功为空）
}

// Journal 是进程内存中的追加日志，所有方法可被并发调用。
type Journal struct {
	mu      sync.Mutex
	seq     int64
	records []Record
	max     int // 最大记录条数，<=0 表示不限
}

// New 创建日志；maxRecords 为日志条数硬上限（<=0 不限）。
func New(maxRecords int) *Journal {
	return &Journal{max: maxRecords}
}

// Append 顺序追加一条记录并返回落盘后的副本。超过上限返回 ErrLimitExceeded，
// 且不会留下半写记录（先判定后写入，单次加锁）。
func (j *Journal) Append(r Record) (Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.max > 0 && len(j.records) >= j.max {
		return Record{}, ErrLimitExceeded
	}
	j.seq++
	r.Seq = j.seq
	j.records = append(j.records, r)
	return r, nil
}

// Read 读回某实例的全部记录，按 Seq（追加顺序）排列。
func (j *Journal) Read(instanceID string) []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Record, 0)
	for _, r := range j.records {
		if r.InstanceID == instanceID {
			out = append(out, r)
		}
	}
	return out
}

// Last 返回某实例最后一条记录；不存在时 ok 为 false。
func (j *Journal) Last(instanceID string) (Record, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := len(j.records) - 1; i >= 0; i-- {
		if j.records[i].InstanceID == instanceID {
			return j.records[i], true
		}
	}
	return Record{}, false
}

// Len 返回日志总条数。
func (j *Journal) Len() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.records)
}

// ErrLimitExceeded 表示追加记录会超过配置的日志最大条数。
var ErrLimitExceeded = errors.New("journal: record limit exceeded")
