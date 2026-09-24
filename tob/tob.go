// Package tob 实现全序广播定序器的核心：按 seq 存放消息的日志、
// 投递游标 deliveredUpTo，以及 Propose/Deliver/Crash/RePropose。
// 依赖 ontology/seq，不依赖 api。
package tob

import (
	"errors"
	"sync"

	"ontology/seq"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrEmptyPayload     = errors.New("tob: empty payload")
	ErrAlreadyDelivered = errors.New("tob: seq already delivered")
	ErrSeqOutOfRange    = errors.New("tob: seq outside allocated gap")
	ErrSlotFilled       = errors.New("tob: slot already occupied")
)

// Log 是定序器全部进程内状态。所有字段都由 mu 保护。
type Log struct {
	mu        sync.Mutex
	counter   *seq.Counter
	delivered int // deliveredUpTo：已按序投递到第几个，初始 0
	entries   map[int]string
	lastScan  int // 非导出：最近一次 Deliver 扫过的日志条目数（O(1) 下标→1）
}

// New 返回空定序器：nextSeq=1，deliveredUpTo=0。
func New() *Log {
	return &Log{counter: seq.NewCounter(), entries: make(map[int]string)}
}

// Propose 分配 seq=nextSeq++ 并记入日志，返回 seq。空 payload 整体失败不留痕。
func (l *Log) Propose(payload string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if payload == "" { // 校验先于一切写操作
		return 0, ErrEmptyPayload
	}
	s := l.counter.Allocate()
	l.entries[s] = payload
	return s, nil
}

// Deliver 仅投递 seq==deliveredUpTo+1 的消息；槽位空（尚未补发）则不投递。
// 直接按下标定位，恰好检视 1 个条目；无消息可投时扫过 0 个。
func (l *Log) Deliver() (int, string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	target := l.delivered + 1
	p, ok := l.entries[target]
	l.lastScan = 1
	if !ok {
		l.lastScan = 0
		return 0, "", false
	}
	l.delivered = target
	return target, p, true
}

// Crash 丢弃所有 seq>deliveredUpTo 的内存日志条目；
// nextSeq（counter）与 deliveredUpTo 保留，形成空洞 (deliveredUpTo, nextSeq)。
func (l *Log) Crash() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for s := range l.entries {
		if s > l.delivered {
			delete(l.entries, s)
		}
	}
	l.lastScan = 0
}

// RePropose 按原始 seq 把崩溃丢失的消息填回空槽位，绝不分配新 seq。
// 三类拒绝互不相同，且任何拒绝都发生在写操作之前。
func (l *Log) RePropose(s int, payload string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s <= l.delivered {
		return ErrAlreadyDelivered
	}
	if s >= l.counter.Next() {
		return ErrSeqOutOfRange
	}
	if _, ok := l.entries[s]; ok {
		return ErrSlotFilled
	}
	l.entries[s] = payload
	return nil
}

// Delivered 返回 deliveredUpTo。并发安全。
func (l *Log) Delivered() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.delivered
}

// nextSeq 仅供同包测试与 SelfCheck 读取。
func (l *Log) nextSeq() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counter.Next()
}

// scanCount 仅供同包白盒测试读取：最近一次 Deliver 扫过的日志条目数。
// 该计数器是非导出字段，不经由任何导出方法暴露。
func (l *Log) scanCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastScan
}
