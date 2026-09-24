// Package broker 维护分区日志与 pid 当前 epoch 表，按顺序执行规则第 1–4 步判定。
// epoch 升级时的「清空全部分区状态」用惰性失效实现：状态条目带有所属 epoch，
// 失配即视为不存在，不做逐分区遍历。
package broker

import (
	"errors"
	"sync"

	"ontology/seqstate"
)

// 可判定的哨兵错误。
var (
	ErrInvalid = errors.New("broker: invalid request")
	ErrFenced  = errors.New("broker: fenced by newer epoch")
)

// Record 是分区日志里的一条记录。
type Record struct {
	Pid, Epoch, Seq, Val int
}

type key struct{ pid, part int }

// slot 是一条 (pid, 分区) 序号状态，epoch 是它所属的当前 epoch。
type slot struct {
	epoch int
	st    *seqstate.State
}

// Broker 是服务端核心，全部状态在进程内存，可并发使用。
type Broker struct {
	mu      sync.Mutex
	n, w    int
	logs    [][]Record
	epochs  map[int]int // pid -> 当前 epoch（无条目 = 从未见过）
	states  map[key]slot
	checked int // 最近一次 Produce 检查/修改过的 (pid,分区) 状态条目个数
}

// New 创建有 partitions 个分区、保留窗口为 window 的 Broker。
func New(partitions, window int) *Broker {
	return &Broker{
		n: partitions, w: window,
		logs:   make([][]Record, partitions),
		epochs: map[int]int{},
		states: map[key]slot{},
	}
}

// Produce 处理一条写请求，按第 1–4 步顺序判定，命中即返回。
// 返回 (位点, 是否重复确认, 错误)；任何错误都不改变任何状态。
func (b *Broker) Produce(pid, epoch, part, seq, val int) (int, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checked = 0
	// 第 1 步：请求非法
	if pid < 0 || epoch < 0 || seq < 0 || part < 0 || part >= b.n {
		return 0, false, ErrInvalid
	}
	cur, seen := b.epochs[pid]
	// 第 2 步：围栏
	if seen && epoch < cur {
		return 0, false, ErrFenced
	}
	// 第 3 步：新 pid 或 epoch 升级，首条必须 seq==0
	if !seen || epoch > cur {
		if seq != 0 {
			return 0, false, seqstate.ErrOutOfOrder
		}
		b.epochs[pid] = epoch // 旧状态靠 slot.epoch 失配惰性失效，不遍历
		cur = epoch
	}
	// 第 4 步：同 epoch 下的序号判定
	k := key{pid, part}
	b.checked++
	sl, ok := b.states[k]
	if !ok || sl.epoch != cur { // 无状态或已随旧 epoch 失效
		if seq != 0 {
			return 0, false, seqstate.ErrOutOfOrder
		}
		sl = slot{cur, seqstate.New(b.w)}
		b.states[k] = sl
	}
	off, dup, err := sl.st.Judge(seq)
	if err != nil || dup {
		return off, dup, err
	}
	off = len(b.logs[part])
	b.logs[part] = append(b.logs[part], Record{pid, epoch, seq, val})
	sl.st.Accept(seq, off)
	return off, false, nil
}

// Log 返回一个分区日志的副本；分区号非法时返回 nil。
func (b *Broker) Log(part int) []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	if part < 0 || part >= b.n {
		return nil
	}
	return append([]Record(nil), b.logs[part]...)
}
