// Package compactlog 实现一个按键压实的日志，删除以墓碑（tombstone）表示，
// 墓碑在保留期内对正在追赶的消费者可见，过期后由压实清除。
package compactlog

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// 可区分的拒绝原因。
var (
	// ErrEmptyKey 表示写入的键为空。
	ErrEmptyKey = errors.New("compactlog: empty key")
	// ErrTimeRegression 表示写入时间早于上一条已接受记录的时间。
	ErrTimeRegression = errors.New("compactlog: time regression")
	// ErrLogFull 表示日志已达到容量上限。
	ErrLogFull = errors.New("compactlog: log is full")
	// ErrConsumerNotSubscribed 表示对未订阅的消费者执行读取。
	ErrConsumerNotSubscribed = errors.New("compactlog: consumer not subscribed")
	// ErrConsumerExists 表示重复订阅同名消费者。
	ErrConsumerExists = errors.New("compactlog: consumer already subscribed")
)

// Record 是日志中的一条记录。Value 为 nil 表示删除墓碑。
type Record struct {
	Seq   int64
	Key   string
	Value []byte
	Time  time.Time
}

// Tombstone 报告该记录是否为删除墓碑。
func (r Record) Tombstone() bool { return r.Value == nil }

type consumer struct {
	nextSeq int64
	view    map[string][]byte
}

// Log 是按键压实的追加日志。压实只留空洞，序号永不重编。
type Log struct {
	mu        sync.RWMutex
	retention time.Duration
	capacity  int
	nextSeq   int64
	lastTime  time.Time
	hasTime   bool
	records   map[int64]Record
	consumers map[string]*consumer
}

// New 创建日志。retention 为墓碑保留期，capacity 为日志可容纳的最大记录数。
func New(retention time.Duration, capacity int) *Log {
	return &Log{
		retention: retention,
		capacity:  capacity,
		records:   make(map[int64]Record),
		consumers: make(map[string]*consumer),
	}
}

// Append 追加一条记录；value 为 nil 表示删除该键。
// 拒绝时不改变时钟、序号计数器、日志与消费者状态。
func (l *Log) Append(key string, value []byte, ts time.Time) (int64, error) {
	return 0, nil
}

// Compact 以 now 为“当前时间”执行压实：删除被同键后续记录覆盖的旧记录，
// 并清除写入时间距今超过保留期的墓碑。
func (l *Log) Compact(now time.Time) {
}

// Subscribe 注册一个从日志起点开始追赶的消费者。
func (l *Log) Subscribe(name string) error {
	return nil
}

// ReadNext 返回消费者应应用的下一条记录（跳过空洞）并应用到其视图。
// ok 为 false 表示消费者已追平。
func (l *Log) ReadNext(name string) (rec Record, ok bool, err error) {
	return Record{}, false, nil
}

// View 返回消费者当前视图的副本。
func (l *Log) View(name string) (map[string][]byte, error) {
	return nil, nil
}

// Records 按序号升序返回当前日志中留存的记录（含墓碑）。
func (l *Log) Records() []Record {
	return nil
}

// sortedSeqs 返回留存记录的升序序号。调用方须持有锁。
func (l *Log) sortedSeqs() []int64 {
	seqs := make([]int64, 0, len(l.records))
	for seq := range l.records {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}
