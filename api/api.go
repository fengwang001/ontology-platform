// Package api 对外门面：事务日志 + read_committed 读取 + 自检。
package api

import (
	"errors"
	"fmt"
	"ontology/rc"
	"ontology/txlog"
	"slices"
)

type Service struct {
	l *txlog.Log
	r *rc.Reader
}

func New() *Service { l := txlog.New(); return &Service{l, rc.New(l)} }

func (s *Service) AppendData(pid int, val string) (int, error) { return s.l.AppendData(pid, val) }
func (s *Service) AppendCommit(pid int) (int, error)           { return s.l.AppendCommit(pid) }
func (s *Service) AppendAbort(pid int) (int, error)            { return s.l.AppendAbort(pid) }
func (s *Service) AdvanceHW(h int) error                       { return s.l.AdvanceHW(h) }
func (s *Service) HW() int                                     { return s.l.HW() }
func (s *Service) LSO() int                                    { return s.l.LSO() }
func (s *Service) Fetch(from int) ([]txlog.Record, int, error) { return s.r.Fetch(from) }

type mtxn struct {
	first, ctrl int
	commit      bool
}

func replay(recs []txlog.Record) (txns []mtxn, owner []int) {
	open := map[int]int{}
	owner = make([]int, len(recs))
	for i, r := range recs {
		owner[i] = -1
		if r.Kind != txlog.Data {
			t := open[r.Pid]
			txns[t].ctrl, txns[t].commit = i, r.Kind == txlog.Commit
			delete(open, r.Pid)
			continue
		}
		if _, ok := open[r.Pid]; !ok {
			open[r.Pid] = len(txns)
			txns = append(txns, mtxn{first: i, ctrl: -1})
		}
		owner[i] = open[r.Pid]
	}
	return txns, owner
}
func modelLSO(recs []txlog.Record, hw int) int {
	txns, _ := replay(recs)
	lso := hw
	for _, t := range txns {
		if t.first < hw && (t.ctrl < 0 || t.ctrl >= hw) && t.first < lso {
			lso = t.first
		}
	}
	return lso
}
func batchCommitted(recs []txlog.Record, hw, to int) (out []string) {
	txns, owner := replay(recs)
	for i := 0; i < to; i++ {
		if o := owner[i]; o >= 0 && txns[o].ctrl >= 0 && txns[o].ctrl < hw && txns[o].commit {
			out = append(out, recs[i].Val)
		}
	}
	return out
}

var script = []txlog.Record{
	{Pid: 1, Val: "a"}, {Pid: 2, Val: "b"}, {Pid: 1, Val: "c"}, {Pid: 1, Kind: txlog.Commit},
	{Pid: 3, Val: "d"}, {Pid: 2, Val: "e"}, {Pid: 2, Kind: txlog.Abort}, {Pid: 3, Val: "f"},
	{Pid: 1, Val: "g"}, {Pid: 3, Kind: txlog.Commit}, {Pid: 1, Kind: txlog.Abort},
}

func appendOp(s *Service, o txlog.Record) (int, error) {
	if o.Kind == txlog.Data {
		return s.AppendData(o.Pid, o.Val)
	}
	if o.Kind == txlog.Commit {
		return s.AppendCommit(o.Pid)
	}
	return s.AppendAbort(o.Pid)
}

// SelfCheck 用内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	svc := New()
	for i, op := range script {
		if off, err := appendOp(svc, op); err != nil || off != i {
			return fmt.Errorf("追加 %d: off=%d err=%v", i, off, err)
		}
	}
	prev, from := 0, 0
	var got []string
	for _, h := range []int{2, 4, 6, 7, 9, 10, 11} {
		if err := svc.AdvanceHW(h); err != nil {
			return err
		}
		lso := svc.LSO() // 不变量 2：合法且单调
		if lso != modelLSO(script, h) || lso > svc.HW() || lso < prev {
			return fmt.Errorf("HW=%d: LSO=%d 非法", h, lso)
		}
		prev = lso
		out, next, err := svc.Fetch(from)
		if err != nil || next != lso {
			return fmt.Errorf("HW=%d: next=%d err=%v", h, next, err)
		}
		for _, r := range out { // 不变量 3：控制标记绝不输出
			if r.Kind != txlog.Data {
				return fmt.Errorf("HW=%d: 输出控制标记", h)
			}
			got = append(got, r.Val)
		}
		from = next
	}
	if want := batchCommitted(script, 11, 11); !slices.Equal(got, want) { // 不变量 1
		return fmt.Errorf("拼接 %v != 批量 %v", got, want)
	}
	// 不变量 4：四类故障注入可判定、互不相同、被拒后状态不变
	svc = New()
	if _, err := svc.AppendData(1, "x"); err != nil {
		return err
	}
	if err := svc.AdvanceHW(1); err != nil {
		return err
	}
	ops := map[error]func() error{
		txlog.ErrInvalidRecord: func() error { _, e := svc.AppendData(0, "y"); return e },
		txlog.ErrNoOpenTxn:     func() error { _, e := svc.AppendAbort(2); return e },
		txlog.ErrInvalidHW:     func() error { return svc.AdvanceHW(5) },
		rc.ErrInvalidFrom:      func() error { _, _, e := svc.Fetch(2); return e },
	}
	if len(ops) != 4 {
		return errors.New("哨兵错误不互不相同")
	}
	for want, op := range ops {
		if err := op(); !errors.Is(err, want) {
			return fmt.Errorf("err=%v want=%v", err, want)
		}
		if svc.HW() != 1 || svc.LSO() != 0 {
			return errors.New("被拒后 HW/LSO 改变")
		}
	}
	if off, err := svc.AppendData(1, "z"); err != nil || off != 1 {
		return fmt.Errorf("被拒后日志改变 off=%d err=%v", off, err)
	}
	return nil
}
