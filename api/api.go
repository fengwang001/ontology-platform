// Package api 是对外接口：构造、批量追加、按时间查位点、日志结束位点与自检。
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/tidx"
	"ontology/tlog"
)

// Store 是按时间戳查位点的只追加日志。
type Store struct{ l *tlog.Log }

// New 以起始位点 base 与容量上限 maxMsgs 构造空日志。
func New(base int64, maxMsgs int) (*Store, error) {
	l, err := tlog.New(base, maxMsgs)
	if err != nil {
		return nil, err
	}
	return &Store{l: l}, nil
}

func (s *Store) Append(ts []int64) (int64, error) { return s.l.Append(ts) }

func (s *Store) Lookup(t int64) (int64, bool) { return s.l.Lookup(t) }

func (s *Store) LEO() int64 { return s.l.LEO() }

// ProbeWithinBound 以布尔结论报告最近一次 Lookup 探查数是否在二分上界内（不暴露数值）。
func (s *Store) ProbeWithinBound() bool { return s.l.ProbeWithinBound() }

// IndexEntries 返回当前时间索引 (TS,Offset) 的副本，仅供演示/自检。
func (s *Store) IndexEntries() []tidx.Entry { return s.l.IndexEntries() }

// SelfCheck 对内置序列（第三节十消息序列 + 随机非单调序列）核验四条不变量，全过返回 nil。
func (s *Store) SelfCheck() error {
	if err := checkErrors(); err != nil {
		return err
	}
	seqs := [][]int64{{50, 40, 70, 60, 70, 65, 90, 80, 90, 85}}
	r := rand.New(rand.NewSource(314))
	for k := 0; k < 8; k++ {
		q := make([]int64, r.Intn(64)+1)
		for i := range q {
			q[i] = int64(r.Intn(20))
		}
		seqs = append(seqs, q)
	}
	for _, seq := range seqs {
		g, err := New(100, 1_000_000)
		if err != nil {
			return err
		}
		if _, err := g.Append(seq); err != nil {
			return err
		}
		if err := checkIndex(g, seq); err != nil {
			return err
		}
		if err := checkNaiveAndMonotonic(g, seq); err != nil {
			return err
		}
	}
	return nil
}

// checkIndex 钉不变量 2：索引 TS/Offset 严格递增，每项 TS=截至该项位点的前缀最大值。
func checkIndex(g *Store, seq []int64) error {
	es := g.IndexEntries()
	var mx int64 = -1
	for j, e := range es {
		if j > 0 && (e.TS <= es[j-1].TS || e.Offset <= es[j-1].Offset) {
			return errors.New("index entries not strictly increasing")
		}
		for k := 0; k <= int(e.Offset-g.l.Base()); k++ {
			if seq[k] > mx {
				mx = seq[k]
			}
		}
		if e.TS != mx {
			return fmt.Errorf("entry %d TS=%d != prefix max %d", j, e.TS, mx)
		}
	}
	return nil
}

// checkNaiveAndMonotonic 钉不变量 1（与逐条扫描在 [min-1,max+1] 逐点一致）与 3（位点随 t 单调）。
func checkNaiveAndMonotonic(g *Store, seq []int64) error {
	base := g.l.Base()
	mn, hi := seq[0], seq[0]
	for _, t := range seq[1:] {
		if t < mn {
			mn = t
		}
		if t > hi {
			hi = t
		}
	}
	prev := base
	for t := mn - 1; t <= hi+1; t++ {
		got, ok := g.Lookup(t)
		want, wok := base+int64(len(seq)), false
		for i, x := range seq {
			if x >= t {
				want, wok = base+int64(i), true
				break
			}
		}
		if got != want || ok != wok {
			return fmt.Errorf("naive mismatch t=%d got=(%d,%v) want=(%d,%v)", t, got, ok, want, wok)
		}
		if got < prev {
			return fmt.Errorf("lookup not monotonic at t=%d", t)
		}
		prev = got
	}
	return nil
}

// checkErrors 钉不变量 4：三类错误互不相同、整批拒绝不留痕、拒绝后仍可用。
func checkErrors() error {
	if _, err := New(-1, 1); !errors.Is(err, tlog.ErrInvalidParam) {
		return fmt.Errorf("negative base: got %v", err)
	}
	if _, err := New(0, 0); !errors.Is(err, tlog.ErrInvalidParam) {
		return fmt.Errorf("non-positive maxMsgs: got %v", err)
	}
	g, err := New(0, 4)
	if err != nil {
		return err
	}
	if _, err := g.Append([]int64{1, 2}); err != nil {
		return err
	}
	leo, nidx := g.LEO(), len(g.IndexEntries())
	if _, err := g.Append([]int64{3, -1}); !errors.Is(err, tlog.ErrNegativeTS) {
		return fmt.Errorf("negative ts: got %v", err)
	}
	if _, err := g.Append([]int64{3, 4, 5}); !errors.Is(err, tlog.ErrCapacity) {
		return fmt.Errorf("capacity: got %v", err)
	}
	if g.LEO() != leo || len(g.IndexEntries()) != nidx {
		return errors.New("state changed after rejected append")
	}
	if f, err := g.Append([]int64{3}); err != nil || f != 2 {
		return fmt.Errorf("use after reject: first=%d err=%v", f, err)
	}
	return nil
}
