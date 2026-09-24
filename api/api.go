// Package api 是幂等生产者序号校验的对外入口。本包只依赖 broker，不反向依赖。
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"

	"ontology/broker"
)

type API struct{ b *broker.Broker }

// New 创建 partitions 个分区、序号保留窗口为 window 的校验器。
func New(partitions, window int) *API { return &API{b: broker.New(partitions, window)} }

func (a *API) Produce(pid, epoch int64, partition int, seq int64, val string) (int64, bool, error) {
	return a.b.Produce(pid, epoch, partition, seq, val)
}

func (a *API) Log(partition int) []broker.Record { return a.b.Log(partition) }

// SelfCheck 用内置请求序列核验四条不变量（含与朴素参照实现的逐条对拍），全部成立返回 nil。
func (a *API) SelfCheck() error {
	ten := []struct {
		pid, ep, seq, off int64
		p                 int
		dup               bool
		err               error
	}{
		{7, 0, 0, 0, 0, false, nil}, {7, 0, 0, 0, 1, false, nil}, {7, 0, 1, 1, 0, false, nil}, {7, 0, 1, 1, 0, true, nil}, {7, 0, 3, 0, 0, false, broker.ErrOutOfOrder},
		{7, 0, 2, 2, 0, false, nil}, {7, 0, 0, 0, 0, false, broker.ErrDuplicateExpired}, {7, 1, 0, 1, 1, false, nil}, {7, 0, 3, 0, 0, false, broker.ErrFenced}, {7, 1, 0, 3, 0, false, nil}}
	b := broker.New(2, 2)
	for i, r := range ten {
		off, dup, err := b.Produce(r.pid, r.ep, r.p, r.seq, "")
		if off != r.off || dup != r.dup || !errors.Is(err, r.err) {
			return fmt.Errorf("ten-step[%d] got (%d,%v,%v) want (%d,%v,%v)", i, off, dup, err, r.off, r.dup, r.err)
		}
	}
	if l0, l1 := b.Log(0), b.Log(1); len(l0) != 4 || len(l1) != 2 {
		return fmt.Errorf("ten-step logs = %d,%d want 4,2", len(l0), len(l1))
	}
	for _, s := range []int64{1, 42, 99} { // 不变量 1：随机序列对拍朴素参照
		if err := crossNaive(s); err != nil {
			return err
		}
	}
	f := broker.New(1, 4) // 不变量 3：围栏后旧 epoch 不落盘
	f.Produce(9, 0, 0, 0, "")
	f.Produce(9, 1, 0, 0, "")
	if _, _, err := f.Produce(9, 0, 0, 0, ""); !errors.Is(err, broker.ErrFenced) || len(f.Log(0)) != 2 {
		return errors.New("fence invariant violated")
	}
	g := broker.New(1, 2) // 不变量 4：seq!=0 的升级不留痕；过期重复不改窗口
	if _, _, err := g.Produce(10, 1, 0, 1, ""); !errors.Is(err, broker.ErrOutOfOrder) {
		return errors.New("bump with seq!=0 must be out-of-order")
	}
	if off, _, err := g.Produce(10, 0, 0, 0, ""); err != nil || off != 0 {
		return errors.New("rejected bump must not change current epoch")
	}
	g.Produce(10, 0, 0, 1, "")
	g.Produce(10, 0, 0, 2, "")
	_, _, eExp := g.Produce(10, 0, 0, 0, "")
	_, _, eNext := g.Produce(10, 0, 0, 3, "")
	if !errors.Is(eExp, broker.ErrDuplicateExpired) || eNext != nil || len(g.Log(0)) != 4 {
		return errors.New("expired duplicate must leave no trace")
	}
	return nil
}

type nstate struct {
	last int64
	win  [][2]int64 // (seq, 位点)；map 中键存在即“该 (pid,分区) 有序号状态”
}
type naive struct {
	n, w int
	logs [][]broker.Record
	ep   map[int64]int64
	st   map[[2]int64]nstate // 键 = (pid, 分区)
}

func newNaive(n, w int) *naive {
	return &naive{n: n, w: w, logs: make([][]broker.Record, n), ep: map[int64]int64{}, st: map[[2]int64]nstate{}}
}

func (z *naive) produce(pid, e int64, p int, seq int64) (int64, bool, error) {
	if pid < 0 || e < 0 || seq < 0 || p < 0 || p >= z.n {
		return 0, false, broker.ErrInvalid
	}
	cur, known := z.ep[pid]
	if known && e < cur {
		return 0, false, broker.ErrFenced
	}
	k := [2]int64{pid, int64(p)}
	if !known || e > cur {
		if seq != 0 {
			return 0, false, broker.ErrOutOfOrder
		}
		z.ep[pid] = e
		for q := 0; q < z.n; q++ { // 朴素清空：逐个分区遍历删除
			delete(z.st, [2]int64{pid, int64(q)})
		}
	}
	s, ok := z.st[k]
	switch {
	case ok && seq == s.last+1:
	case ok && seq > s.last+1:
		return 0, false, broker.ErrOutOfOrder
	case ok:
		for _, x := range s.win { // 线性查找保留窗口
			if x[0] == seq {
				return x[1], true, nil
			}
		}
		return 0, false, broker.ErrDuplicateExpired
	case seq != 0:
		return 0, false, broker.ErrOutOfOrder
	}
	off := int64(len(z.logs[p]))
	z.logs[p] = append(z.logs[p], broker.Record{Pid: pid, Epoch: e, Partition: p, Seq: seq})
	s.last, s.win = seq, append(s.win, [2]int64{seq, off})
	if len(s.win) > z.w {
		s.win = s.win[1:]
	}
	z.st[k] = s
	return off, false, nil
}

func crossNaive(seed int64) error {
	const N, W, R = 3, 2, 200
	real, ref := broker.New(N, W), newNaive(N, W)
	rnd := rand.New(rand.NewSource(seed))
	for i := 0; i < R; i++ {
		pid, ep := int64(rnd.Intn(3)), int64(rnd.Intn(3))
		p, seq := rnd.Intn(N), int64(rnd.Intn(5))
		o1, d1, e1 := real.Produce(pid, ep, p, seq, "")
		o2, d2, e2 := ref.produce(pid, ep, p, seq)
		if o1 != o2 || d1 != d2 || !errors.Is(e1, e2) {
			return fmt.Errorf("naive seed=%d i=%d req=(%d,%d,%d,%d) got=%v ref=%v", seed, i, pid, ep, p, seq, e1, e2)
		}
		got, want := real.Log(p), ref.logs[p]
		if !slices.Equal(got, want) {
			return fmt.Errorf("naive seed=%d p=%d log %v != %v", seed, p, got, want)
		}
	}
	return nil
}
