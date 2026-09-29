package pkchange

import (
	"log/slog"
	"sync"
)

// DefaultBatchLimit 是单批变更条数的默认上限。
const DefaultBatchLimit = 1000

// DefaultPartitions 是默认分区数。
const DefaultPartitions = 8

// Store 持有源表与下游视图，提供原子的批处理与并发读。
//
// 串行化所有 Apply；任意时刻的并发读只会看到「提交前」或「提交后」的完整状态，
// 且下游视图与源表始终是同一份快照。
type Store struct {
	mu             sync.RWMutex
	source         map[string]Row
	view           map[string]Row
	partitionCount int
	batchLimit     int
	emitted        []Event // 已产出事件的确定序列（仅追加，仅在写锁内变更）
	logger         *slog.Logger
}

// NewStore 创建空 Store；partitionCount 与 batchLimit 传 0 使用默认值。
func NewStore(partitionCount, batchLimit int, logger *slog.Logger) *Store {
	if partitionCount <= 0 {
		partitionCount = DefaultPartitions
	}
	if batchLimit <= 0 {
		batchLimit = DefaultBatchLimit
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{
		source:         map[string]Row{},
		view:           map[string]Row{},
		partitionCount: partitionCount,
		batchLimit:     batchLimit,
		logger:         logger,
	}
}

// Apply 原子地校验、拆分、合并、分区投递一批变更。
// 任一条变更非法时整批拒绝，源表、下游视图与已产出事件均不变。
func (s *Store) Apply(changes []Change) ([]Partition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("pkchange: batch input",
		"batch_size", len(changes),
		"changes", describeChanges(changes),
	)

	var accepted []logChange
	raw, err := Split(s.source, changes, s.batchLimit, func(idx int, ch Change) {
		accepted = append(accepted, logChange{Index: idx, Change: ch})
	})
	if err != nil {
		rj, _ := AsReject(err)
		args := []any{"error", err.Error()}
		if rj != nil {
			args = append(args, "reason", string(rj.Reason), "change_index", rj.Index, "key", rj.Key)
		}
		s.logger.Warn("pkchange: batch rejected, no state changed", args...)
		return nil, err
	}

	s.logger.Info("pkchange: split result",
		"accepted", describeAccepted(accepted),
		"events", describeEvents(raw),
	)

	merged := Merge(raw)
	partitions := PartitionByKey(merged, s.partitionCount)

	s.logger.Info("pkchange: merged and partitioned output",
		"merged_events", describeEvents(merged),
		"partition_count", s.partitionCount,
		"partitions", describePartitions(partitions),
	)

	// 基于本地副本计算新状态，全部成功后再替换引用，保证原子提交。
	newSource := applyEvents(s.source, merged)
	newView := applyEvents(s.view, merged)

	newEmitted := make([]Event, len(s.emitted), len(s.emitted)+len(merged))
	copy(newEmitted, s.emitted)
	newEmitted = append(newEmitted, merged...)

	s.source = newSource
	s.view = newView
	s.emitted = newEmitted

	s.logger.Info("pkchange: batch committed",
		"source_size", len(newSource),
		"view_size", len(newView),
		"emitted_total", len(newEmitted),
	)

	return clonePartitions(partitions), nil
}

// SourceSnapshot 返回源表的深拷贝快照，调用方可自由修改。
func (s *Store) SourceSnapshot() map[string]Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.source)
}

// ViewSnapshot 返回下游视图的深拷贝快照。
func (s *Store) ViewSnapshot() map[string]Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.view)
}

// Snapshots 在同一次读锁内返回源表与下游视图快照，
// 保证并发读看到的两者必然来自同一个已提交版本。
func (s *Store) Snapshots() (source, view map[string]Row) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.source), cloneMap(s.view)
}

// EmittedEvents 返回已产出事件（合并后）的完整确定序列的拷贝。
func (s *Store) EmittedEvents() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, len(s.emitted))
	for i, ev := range s.emitted {
		out[i] = cloneEvent(ev)
	}
	return out
}

// PartitionCount 返回分区数。
func (s *Store) PartitionCount() int {
	return s.partitionCount
}

// applyEvents 按序把事件应用到快照副本，返回新快照。
func applyEvents(snapshot map[string]Row, events []Event) map[string]Row {
	out := cloneMap(snapshot)
	for _, ev := range events {
		switch ev.Kind {
		case EventWrite:
			out[ev.Key] = cloneRow(ev.Data)
		case EventDelete:
			delete(out, ev.Key)
		}
	}
	return out
}

func cloneMap(m map[string]Row) map[string]Row {
	out := make(map[string]Row, len(m))
	for k, row := range m {
		out[k] = cloneRow(row)
	}
	return out
}

func clonePartitions(partitions []Partition) []Partition {
	out := make([]Partition, len(partitions))
	for i, p := range partitions {
		out[i] = Partition{Index: p.Index, Events: make([]Event, len(p.Events))}
		for j, ev := range p.Events {
			out[i].Events[j] = cloneEvent(ev)
		}
	}
	return out
}
