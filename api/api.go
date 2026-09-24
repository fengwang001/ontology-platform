// Package api 是幂等生产者序号校验的对外包。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/broker"
	"ontology/seqstate"
)

var (
	ErrInvalid          = broker.ErrInvalid
	ErrFenced           = broker.ErrFenced
	ErrOutOfOrder       = seqstate.ErrOutOfOrder
	ErrDuplicateExpired = seqstate.ErrDuplicateExpired
) // 四类可判定的哨兵错误，互不相同；重复确认不是错误。

type Record = broker.Record

// Server 是服务端句柄，可并发使用。
type Server struct {
	b    *broker.Broker
	n, w int
}

// New 创建有 partitions 个分区、保留窗口为 window 的服务端。
func New(partitions, window int) *Server {
	if partitions < 1 || window < 1 {
		panic("api: partitions and window must be >= 1")
	}
	return &Server{b: broker.New(partitions, window), n: partitions, w: window}
}

// Produce 处理一条写请求，返回 (位点, 是否重复确认, 错误)。
func (s *Server) Produce(pid, epoch, partition, seq, val int) (int, bool, error) {
	return s.b.Produce(pid, epoch, partition, seq, val)
}

// Log 返回一个分区日志的副本。
func (s *Server) Log(partition int) []Record { return s.b.Log(partition) }

// SelfCheck 对内置请求序列核验四条不变量，全部通过返回 nil。
func (s *Server) SelfCheck() error {
	ten := []req{ // 第三节的十步，pid=7，N=2，W=2
		{7, 0, 0, 0, 0}, {7, 0, 1, 0, 0}, {7, 0, 0, 1, 0}, {7, 0, 0, 1, 0}, {7, 0, 0, 3, 0},
		{7, 0, 0, 2, 0}, {7, 0, 0, 0, 0}, {7, 1, 1, 0, 0}, {7, 0, 0, 3, 0}, {7, 1, 0, 0, 0}}
	if err := replayAndCheck(New(2, 2), ten); err != nil {
		return err
	}
	return replayAndCheck(New(4, 3), lcgReqs(4000, 5, 3, 4))
}

type req struct{ pid, epoch, part, seq, val int }

// replayAndCheck 核验不变量 1/2/3；不变量 4 由比对隐含：参照出错时不改状态而结果仍逐条相同。
func replayAndCheck(sv *Server, reqs []req) error {
	nv := newNaive(sv.n, sv.w)
	for i, r := range reqs {
		o1, d1, e1 := sv.Produce(r.pid, r.epoch, r.part, r.seq, r.val)
		o2, d2, e2 := nv.step(r.pid, r.epoch, r.part, r.seq, r.val)
		if o1 != o2 || d1 != d2 || (e1 == nil) != (e2 == nil) || !errors.Is(e1, e2) {
			return fmt.Errorf("selfcheck: req %d: (%d,%v,%v) != (%d,%v,%v)", i, o1, d1, e1, o2, d2, e2)
		}
	}
	maxEpoch, next := map[[2]int]int{}, map[[3]int]int{}
	for p := 0; p < sv.n; p++ {
		lg := sv.Log(p)
		if !reflect.DeepEqual(lg, nv.logs[p]) {
			return fmt.Errorf("selfcheck: log %d differs", p)
		}
		for _, rec := range lg {
			kp, k := [2]int{rec.Pid, p}, [3]int{rec.Pid, rec.Epoch, p}
			if rec.Epoch < maxEpoch[kp] || rec.Seq != next[k] {
				return fmt.Errorf("selfcheck: log %d invariant broken: %+v", p, rec)
			}
			maxEpoch[kp] = rec.Epoch
			next[k]++
		}
	}
	return nil
}

func lcgReqs(count, pids, epochs, parts int) []req {
	r := make([]req, count)
	x := 12345
	for i := range r {
		x = x*1103515245 + 12345
		v := x >> 16 & 0x7fff
		r[i] = req{v % pids, v % epochs, v % parts, v % 9, i}
	}
	return r
}

// naive 是朴素参照：升级 epoch 时逐分区遍历清空，窗口线性查找。
type naive struct {
	n, w   int
	logs   [][]Record
	epochs map[int]int
	last   map[[2]int]int // 无条目 = 无序号状态
	win    map[[2]int][][2]int
}

func newNaive(n, w int) *naive {
	return &naive{n: n, w: w, logs: make([][]Record, n), epochs: map[int]int{},
		last: map[[2]int]int{}, win: map[[2]int][][2]int{}}
}

func (x *naive) step(pid, epoch, part, seq, val int) (int, bool, error) {
	if pid < 0 || epoch < 0 || seq < 0 || part < 0 || part >= x.n {
		return 0, false, ErrInvalid
	}
	cur, seen := x.epochs[pid]
	if seen && epoch < cur {
		return 0, false, ErrFenced
	}
	k := [2]int{pid, part}
	if !seen || epoch > cur {
		if seq != 0 {
			return 0, false, ErrOutOfOrder
		}
		x.epochs[pid] = epoch
		for p := 0; p < x.n; p++ { // 逐分区清空
			kp := [2]int{pid, p}
			delete(x.last, kp)
			delete(x.win, kp)
		}
	}
	last, has := x.last[k]
	if has && seq <= last {
		for _, e := range x.win[k] {
			if e[0] == seq {
				return e[1], true, nil
			}
		}
		return 0, false, ErrDuplicateExpired
	}
	if (has && seq > last+1) || (!has && seq != 0) {
		return 0, false, ErrOutOfOrder
	}
	off := len(x.logs[part])
	x.logs[part] = append(x.logs[part], Record{Pid: pid, Epoch: epoch, Seq: seq, Val: val})
	x.last[k] = seq
	x.win[k] = append(x.win[k], [2]int{seq, off})
	if len(x.win[k]) > x.w {
		x.win[k] = x.win[k][1:]
	}
	return off, false, nil
}
