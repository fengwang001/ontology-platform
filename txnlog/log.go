// Package txnlog 实现事务消息分区日志：连续位点追加、提交/中止标记、
// 只进不退的高水位与稳定位点，以及可复现的已提交读。
package txnlog

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因。任何一次被拒都不会改变日志、事务表、高水位或稳定位点。
var (
	ErrUnknownProducer      = errors.New("txnlog: unknown producer")
	ErrNoOngoingTransaction = errors.New("txnlog: no ongoing transaction for producer")
	ErrInvalidHighWaterMark = errors.New("txnlog: invalid high water mark")
	ErrInvalidReadStart     = errors.New("txnlog: invalid read start offset")
)

// RecordType 区分数据记录与控制标记。
type RecordType uint8

const (
	DataRecord RecordType = iota
	CommitMarker
	AbortMarker
)

func (t RecordType) String() string {
	switch t {
	case DataRecord:
		return "data"
	case CommitMarker:
		return "commit"
	case AbortMarker:
		return "abort"
	}
	return "unknown"
}

// Record 是日志中一条记录的只读视图。
type Record struct {
	Offset   uint64
	Type     RecordType
	Producer string
	Payload  []byte
}

type entry struct {
	Record
	resolved  bool // 所属事务是否已有可见结论
	committed bool // 所属事务是否以提交结束
}

// transaction 记录一个事务的首位点与结束标记位点（-1 表示仍在进行）。
type transaction struct {
	producer    string
	firstOffset uint64
	endOffset   int64
	indices     []int
}

type producerState struct {
	open *transaction
}

// Log 是单分区事务日志，所有方法均可并发调用。
type Log struct {
	mu        sync.RWMutex
	entries   []entry
	producers map[string]*producerState
	txns      []*transaction // 按首位点递增
	hwm       uint64
	stable    uint64
	blocker   string // 稳定位点判定依据
}

// NewLog 以合法生产者集合创建空日志。
func NewLog(validProducers ...string) *Log {
	l := &Log{producers: make(map[string]*producerState, len(validProducers))}
	for _, p := range validProducers {
		l.producers[p] = &producerState{}
	}
	l.blocker = "high water mark"
	return l
}
