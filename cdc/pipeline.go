package cdc

import (
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"sync"
)

// maxKeyLength 是合法主键的最大长度。
const maxKeyLength = 128

// Pipeline 是主键变更的拆分与分区投递组件。
//
// 工作流程：Apply 接收一批源表变更，先逐条校验（任何一条非法
// 则整批拒绝且无副作用），再拆分为写入/删除事件序列，按主键
// 合并（同一主键只保留拆分序列中的最后一条），最后按主键哈希
// 分区投递并提交源表与下游视图。
//
// 所有状态由读写锁保护，Snapshot 可在任意并发度下读到
// 源表与下游视图一致的自洽快照。
type Pipeline struct {
	mu           sync.RWMutex
	numParts     int
	maxBatchRows int
	logger       *log.Logger

	source     map[string]Row // 源表当前状态
	downstream map[string]Row // 下游视图（由已投递事件重放得到）
	events     []Event        // 已产出事件（合并后，按产出顺序）
	parts      [][]Event      // 各分区已投递的事件
}

// New 创建一个投递组件。numPartitions 为分区数（>=1），
// maxBatchRows 为单批最大变更行数（>=1），logger 可为 nil。
func New(numPartitions, maxBatchRows int, logger *log.Logger) *Pipeline {
	if numPartitions < 1 {
		numPartitions = 1
	}
	if maxBatchRows < 1 {
		maxBatchRows = 1
	}
	p := &Pipeline{
		numParts:     numPartitions,
		maxBatchRows: maxBatchRows,
		logger:       logger,
		source:       make(map[string]Row),
		downstream:   make(map[string]Row),
		parts:        make([][]Event, numPartitions),
	}
	return p
}

// Snapshot 是某一时刻的一致性快照：源表、下游视图与已产出事件。
type Snapshot struct {
	Source     map[string]Row
	Downstream map[string]Row
	Events     []Event
	Partitions [][]Event
}

// Apply 校验并应用一批变更，返回本批按分区投递的事件。
// 任何一条变更非法都会整批拒绝并返回 *RejectError，
// 此时源表、下游视图与已产出事件均不发生任何变化。
func (p *Pipeline) Apply(batch []Change) ([][]Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logf("输入批: %s", formatBatch(batch))

	working, err := p.validate(batch)
	if err != nil {
		p.logf("整批拒绝: %v", err)
		return nil, err
	}

	events := merge(split(batch))
	parts := dispatch(events, p.numParts)
	p.logf("拆分合并: %s", formatEvents(events))
	p.logf("分区输出: %s", formatPartitions(parts))

	p.source = working
	p.downstream = replay(p.downstream, events)
	p.events = append(p.events, events...)
	for i := range parts {
		p.parts[i] = append(p.parts[i], parts[i]...)
	}
	return clonePartitions(parts), nil
}

// Snapshot 返回当前状态的一致性深拷贝快照，可并发调用。
func (p *Pipeline) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return Snapshot{
		Source:     cloneTable(p.source),
		Downstream: cloneTable(p.downstream),
		Events:     cloneEvents(p.events),
		Partitions: clonePartitions(p.parts),
	}
}

// validate 在源表的副本上逐条校验并预演整批变更，
// 全部合法时返回预演后的新源表；任何一条非法即返回
// 带可区分原因的 *RejectError，调用方不得提交任何状态。
func (p *Pipeline) validate(batch []Change) (map[string]Row, error) {
	if len(batch) > p.maxBatchRows {
		return nil, &RejectError{
			Reason: ErrTooManyRows,
			Index:  -1,
			Detail: fmt.Sprintf("批内 %d 行超过上限 %d", len(batch), p.maxBatchRows),
		}
	}
	working := cloneTable(p.source)
	for i, ch := range batch {
		if err := checkKey(ch.Key); err != nil {
			return nil, &RejectError{Reason: ErrInvalidPrimaryKey, Index: i, Key: ch.Key, Detail: err.Error()}
		}
		_, exists := working[ch.Key]
		switch ch.Kind {
		case Insert:
			if exists {
				return nil, &RejectError{Reason: ErrKeyAlreadyExists, Index: i, Key: ch.Key, Detail: "插入的主键已存在"}
			}
			working[ch.Key] = ch.Row
		case Delete:
			if !exists {
				return nil, &RejectError{Reason: ErrKeyNotFound, Index: i, Key: ch.Key, Detail: "删除的主键不存在"}
			}
			delete(working, ch.Key)
		case Update:
			if !pkChanged(ch) {
				if !exists {
					return nil, &RejectError{Reason: ErrKeyNotFound, Index: i, Key: ch.Key, Detail: "更新的主键不存在"}
				}
				working[ch.Key] = ch.Row
				continue
			}
			if err := checkKey(ch.OldKey); err != nil {
				return nil, &RejectError{Reason: ErrInvalidPrimaryKey, Index: i, Key: ch.OldKey, Detail: "旧主键非法: " + err.Error()}
			}
			if _, ok := working[ch.OldKey]; !ok {
				return nil, &RejectError{Reason: ErrKeyNotFound, Index: i, Key: ch.OldKey, Detail: "旧主键不存在"}
			}
			if exists {
				return nil, &RejectError{Reason: ErrKeyAlreadyExists, Index: i, Key: ch.Key, Detail: "主键变更后的新键已存在"}
			}
			delete(working, ch.OldKey)
			working[ch.Key] = ch.Row
		default:
			return nil, &RejectError{Reason: ErrInvalidPrimaryKey, Index: i, Key: ch.Key, Detail: fmt.Sprintf("未知变更类型 %d", ch.Kind)}
		}
	}
	return working, nil
}

// checkKey 校验主键合法性：非空且不超过长度上限。
func checkKey(key string) error {
	if key == "" {
		return fmt.Errorf("主键为空")
	}
	if len(key) > maxKeyLength {
		return fmt.Errorf("主键长度 %d 超过上限 %d", len(key), maxKeyLength)
	}
	return nil
}

// replay 把合并后的事件重放到下游视图副本上，返回新视图。
func replay(view map[string]Row, events []Event) map[string]Row {
	next := cloneTable(view)
	for _, ev := range events {
		if ev.Op == OpDelete {
			delete(next, ev.Key)
		} else {
			next[ev.Key] = ev.Row
		}
	}
	return next
}

func cloneTable(t map[string]Row) map[string]Row {
	out := make(map[string]Row, len(t))
	for k, row := range t {
		cp := make(Row, len(row))
		for f, v := range row {
			cp[f] = v
		}
		out[k] = cp
	}
	return out
}

func cloneEvents(events []Event) []Event {
	out := make([]Event, len(events))
	copy(out, events)
	return out
}

func clonePartitions(parts [][]Event) [][]Event {
	out := make([][]Event, len(parts))
	for i := range parts {
		out[i] = cloneEvents(parts[i])
	}
	return out
}

func (p *Pipeline) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}

func formatBatch(batch []Change) string {
	var b strings.Builder
	for i, ch := range batch {
		fmt.Fprintf(&b, "\n  [%d] %s key=%q oldKey=%q row=%v", i, ch.Kind, ch.Key, ch.OldKey, ch.Row)
	}
	return b.String()
}

func formatEvents(events []Event) string {
	var b strings.Builder
	for _, ev := range events {
		fmt.Fprintf(&b, "\n  seq=%d %s key=%q row=%v", ev.Seq, ev.Op, ev.Key, ev.Row)
	}
	return b.String()
}

func formatPartitions(parts [][]Event) string {
	var b strings.Builder
	for i, part := range parts {
		fmt.Fprintf(&b, "\n  partition-%d:", i)
		for _, ev := range part {
			fmt.Fprintf(&b, " [seq=%d %s key=%q]", ev.Seq, ev.Op, ev.Key)
		}
	}
	return b.String()
}

// split 把一批变更拆分为下游事件序列，并赋予批内序号。
// 规则：插入 -> 写入；删除 -> 删除；主键不变的更新 -> 一次写入；
// 主键变化的更新 -> 先删除旧键，再写入新键。
func split(batch []Change) []Event {
	var out []Event
	emit := func(op Op, key string, row Row) {
		out = append(out, Event{Seq: len(out), Op: op, Key: key, Row: row})
	}
	for _, ch := range batch {
		switch ch.Kind {
		case Insert:
			emit(OpWrite, ch.Key, ch.Row)
		case Delete:
			emit(OpDelete, ch.Key, nil)
		case Update:
			if pkChanged(ch) {
				emit(OpDelete, ch.OldKey, nil)
			}
			emit(OpWrite, ch.Key, ch.Row)
		}
	}
	return out
}

// pkChanged 判断一次更新是否发生主键变更。
func pkChanged(ch Change) bool {
	return ch.OldKey != "" && ch.OldKey != ch.Key
}

// merge 按主键合并事件序列：同一主键只保留它在拆分序列中的
// 最后一条，其余丢弃；保留下来的事件维持原先后顺序。
func merge(events []Event) []Event {
	last := make(map[string]int, len(events))
	for i, ev := range events {
		last[ev.Key] = i
	}
	out := make([]Event, 0, len(last))
	for i, ev := range events {
		if last[ev.Key] == i {
			out = append(out, ev)
		}
	}
	return out
}

// partitionOf 用 FNV-1a 哈希把主键稳定映射到分区，
// 同一主键永远落入同一分区，保证计算结果可重复。
func partitionOf(key string, numParts int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(numParts))
}

// dispatch 把合并后的事件按主键哈希分区，各分区内保持事件顺序。
func dispatch(events []Event, numParts int) [][]Event {
	out := make([][]Event, numParts)
	for _, ev := range events {
		p := partitionOf(ev.Key, numParts)
		out[p] = append(out[p], ev)
	}
	return out
}
