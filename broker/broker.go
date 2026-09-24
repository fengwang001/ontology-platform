// Package broker 持有 N 条只追加分区日志、pid → 当前 epoch 表，
// 并按规则给定的顺序执行第 1–4 步判定；epoch 升级用 epoch 标签惰性失效，
// 不逐个分区遍历（因此复杂度不随分区数增长）。本包只依赖 seqstate。
package broker

import (
	"errors"
	"sync"

	"ontology/seqstate"
)

// 四类可判定、互不相同的哨兵错误；重复确认不是错误。
var (
	ErrInvalid          = errors.New("invalid request")
	ErrFenced           = errors.New("producer epoch fenced")
	ErrOutOfOrder       = errors.New("sequence out of order")
	ErrDuplicateExpired = errors.New("duplicate outside retention window")
)

// Record 是分区日志中的一条已接受记录。
type Record struct {
	Pid       int64
	Epoch     int64
	Partition int
	Seq       int64
	Val       string
}

// Broker 是进程内、并发安全的分区日志与序号校验器。
type Broker struct {
	mu         sync.Mutex
	partitions int
	window     int
	logs       [][]Record
	epochs     map[int64]int64
	states     map[key]*entry
	// checked 记录最近一次 Produce 检查或修改过的序号状态条目数。
	// 非导出：仅包内白盒测试可读，绝不经任何导出接口暴露。
	checked int
}

type key struct {
	pid       int64
	partition int
}

// entry 惰性包一层 epoch 标签：标签落后于 pid 当前 epoch 的条目视为“已被清空”，
// 下次取用时 O(1) 重置，等价于朴素实现升级时遍历全部分区逐个删除。
type entry struct {
	epoch int64
	state *seqstate.State
}

// New 创建 partitions 个分区、序号保留窗口为 window 的 Broker。
func New(partitions, window int) *Broker {
	return &Broker{
		partitions: partitions,
		window:     window,
		logs:       make([][]Record, partitions),
		epochs:     make(map[int64]int64),
		states:     make(map[key]*entry),
	}
}

// Produce 按规则固定顺序处理写请求，返回 (位点, 是否重复确认, error)。
// 任何错误分支都位于所有状态写入之前，被拒请求不留任何痕迹。
func (b *Broker) Produce(pid, epoch int64, partition int, seq int64, val string) (int64, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checked = 0

	// 第 1 步：请求非法。
	if pid < 0 || epoch < 0 || seq < 0 || partition < 0 || partition >= b.partitions {
		return 0, false, ErrInvalid
	}
	k := key{pid: pid, partition: partition}
	cur, known := b.epochs[pid]

	// 第 2 步：旧 epoch（含僵尸实例）围栏，发生在任何写操作之前。
	if known && epoch < cur {
		return 0, false, ErrFenced
	}

	// 第 3 步：首次出现或更新的 epoch；只有走到这里才可能改写 epoch 表。
	if !known || epoch > cur {
		if seq != 0 { // 首条不为 0：epoch 表与序号状态一律不动
			return 0, false, ErrOutOfOrder
		}
		b.epochs[pid] = epoch // 全分区序号状态的“清空”由下方惰性标签完成
		st := b.obtain(k, epoch)
		off := int64(len(b.logs[partition]))
		b.logs[partition] = append(b.logs[partition], Record{pid, epoch, partition, seq, val})
		st.Commit(seq, off)
		return off, false, nil
	}

	// 第 4 步：epoch == 当前 epoch，按 (pid, 分区) 各自独立的序号状态判定。
	st := b.obtain(k, cur)
	dec, oldOff := st.Check(seq)
	switch dec {
	case seqstate.Append:
		off := int64(len(b.logs[partition]))
		b.logs[partition] = append(b.logs[partition], Record{pid, epoch, partition, seq, val})
		st.Commit(seq, off)
		return off, false, nil
	case seqstate.Duplicate:
		return oldOff, true, nil // 重复确认：返回旧位点，不追加，不改状态
	case seqstate.ExpiredDuplicate:
		return 0, false, ErrDuplicateExpired
	default:
		return 0, false, ErrOutOfOrder
	}
}

// obtain 取目标条目的当前状态；命中惰性失效标签时当场 O(1) 重置。
// 每次 Produce 只取用这一个 (pid, 分区) 条目，故 checked 恒不超过 1。
func (b *Broker) obtain(k key, ep int64) *seqstate.State {
	e, ok := b.states[k]
	b.checked++
	if !ok {
		e = &entry{epoch: ep, state: seqstate.New(b.window)}
		b.states[k] = e
	} else if e.epoch != ep {
		e.epoch = ep
		e.state.Reset()
	}
	return e.state
}

// Log 返回某分区日志的快照副本，可与 Produce 并发调用。
func (b *Broker) Log(partition int) []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	if partition < 0 || partition >= b.partitions {
		return nil
	}
	out := make([]Record, len(b.logs[partition]))
	copy(out, b.logs[partition])
	return out
}
