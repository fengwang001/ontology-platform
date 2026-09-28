package cdc

import (
	"fmt"
	"hash/fnv"
	"sync"
)

// Pipeline 主键变更拆分与分区投递管线。
// 校验 -> 拆分 -> 合并 -> 哈希分区 -> 原子提交（源表 + 下游视图 + 事件日志）。
type Pipeline struct {
	mu           sync.RWMutex
	maxBatchRows int
	partitions   int
	source       map[string]Row
	view         map[string]Row
	events       []Event
	partitioned  [][]Event
	seq          uint64
}

// NewPipeline 创建管线。maxBatchRows 为批行数上限，partitions 为分区数。
func NewPipeline(maxBatchRows, partitions int) *Pipeline {
	if maxBatchRows < 1 {
		maxBatchRows = 1
	}
	if partitions < 1 {
		partitions = 1
	}
	return &Pipeline{
		maxBatchRows: maxBatchRows,
		partitions:   partitions,
		source:       make(map[string]Row),
		view:         make(map[string]Row),
		partitioned:  make([][]Event, partitions),
	}
}

// ApplyBatch 校验并应用一批变更。任一条非法则整批拒绝，
// 源表、下游视图与已产出事件均不变。
func (p *Pipeline) ApplyBatch(changes []Change) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(changes) > p.maxBatchRows {
		return &BatchRejectedError{
			Index:  -1,
			Reason: ReasonTooManyRows,
			Detail: fmt.Sprintf("batch has %d rows, limit is %d", len(changes), p.maxBatchRows),
		}
	}

	// 在工作副本上逐条校验并拆分，任何一条失败即整批拒绝。
	working := make(map[string]Row, len(p.source)+len(changes))
	for k, v := range p.source {
		working[k] = v
	}
	var split []Event
	for i, c := range changes {
		if err := validateAndApply(working, c); err != nil {
			err.Index = i
			return err
		}
		split = append(split, splitChange(c)...)
	}

	// 合并：同一个键只保留拆分序列中的最后一条。
	merged := mergeEvents(split)

	// 提交：分配序号、按主键哈希分区、原子更新源表与下游视图。
	for _, e := range merged {
		p.seq++
		e.Seq = p.seq
		p.events = append(p.events, e)
		part := p.partitionOf(e.Key)
		p.partitioned[part] = append(p.partitioned[part], e)
	}
	p.source = working
	for _, e := range merged {
		if e.Type == EventWrite {
			p.view[e.Key] = e.Row
		} else {
			delete(p.view, e.Key)
		}
	}
	return nil
}

// validateAndApply 针对工作副本校验单条变更，校验通过则应用到工作副本。
func validateAndApply(working map[string]Row, c Change) *BatchRejectedError {
	reject := func(reason RejectReason, format string, args ...any) *BatchRejectedError {
		return &BatchRejectedError{Index: -1, Reason: reason, Detail: fmt.Sprintf(format, args...)}
	}
	switch c.Type {
	case ChangeInsert:
		if c.After.Key == "" {
			return reject(ReasonInvalidKey, "insert with empty primary key")
		}
		if _, ok := working[c.After.Key]; ok {
			return reject(ReasonKeyExists, "insert key %q already exists", c.After.Key)
		}
		working[c.After.Key] = c.After
	case ChangeDelete:
		if c.Before.Key == "" {
			return reject(ReasonInvalidKey, "delete with empty primary key")
		}
		if _, ok := working[c.Before.Key]; !ok {
			return reject(ReasonKeyNotFound, "delete key %q does not exist", c.Before.Key)
		}
		delete(working, c.Before.Key)
	case ChangeUpdate:
		if c.Before.Key == "" || c.After.Key == "" {
			return reject(ReasonInvalidKey, "update with empty primary key (before=%q after=%q)", c.Before.Key, c.After.Key)
		}
		if _, ok := working[c.Before.Key]; !ok {
			return reject(ReasonKeyNotFound, "update key %q does not exist", c.Before.Key)
		}
		if c.Before.Key != c.After.Key {
			if _, ok := working[c.After.Key]; ok {
				return reject(ReasonKeyExists, "update moves key %q to existing key %q", c.Before.Key, c.After.Key)
			}
			delete(working, c.Before.Key)
		}
		working[c.After.Key] = c.After
	default:
		return reject(ReasonInvalidKey, "unknown change type %d", int(c.Type))
	}
	return nil
}

// partitionOf 返回主键哈希到的分区号（FNV-1a，确定性）。
func (p *Pipeline) partitionOf(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(p.partitions))
}

// PartitionOf 返回主键哈希到的分区号（FNV-1a）。
func (p *Pipeline) PartitionOf(key string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.partitionOf(key)
}

// Snapshot 返回源表与下游视图的一致性快照（同一锁内读取）。
func (p *Pipeline) Snapshot() (source, view map[string]Row) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return copyRows(p.source), copyRows(p.view)
}

// Events 返回已产出的事件日志副本。
func (p *Pipeline) Events() []Event {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Event(nil), p.events...)
}

// PartitionEvents 返回指定分区的事件序列副本。
func (p *Pipeline) PartitionEvents(partition int) []Event {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if partition < 0 || partition >= p.partitions {
		return nil
	}
	return append([]Event(nil), p.partitioned[partition]...)
}

func copyRows(m map[string]Row) map[string]Row {
	out := make(map[string]Row, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
