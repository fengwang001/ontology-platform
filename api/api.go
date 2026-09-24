// Package api 是幂等 sink 的对外入口，依赖 sink。
package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"

	"ontology/sink"
	"ontology/wm"
)

// Sink 并发安全；Write 整批原子，读方法可并发。
type Sink struct {
	maxP int
	sk   *sink.Sink
}

func New(maxPartitions int) *Sink {
	return &Sink{maxP: maxPartitions, sk: sink.New()}
}

func (a *Sink) Write(batch []wm.Rec) error { return a.sk.Commit(batch, a.maxP) }
func (a *Sink) Restart()                   { a.sk.Restart() }
func (a *Sink) Table() map[string]int64    { return a.sk.Table() }
func (a *Sink) Watermark(p int) int64      { return a.sk.Watermark(p) }
func (a *Sink) Duplicates() int64          { return a.sk.Duplicates() }

// SelfCheck 用一组内置批序列核验第二节四条不变量，全部通过返回 nil。
func (a *Sink) SelfCheck() error {
	const P, L, steps = 4, 60, 200
	keyOf := func(p, off int) string { return fmt.Sprintf("k%d", (p*7+off)%3) }
	valOf := func(p, off int) int64 { return int64((p+1)*(off+3)) + 1 }

	run := func(withRestarts bool) (map[string]int64, []int64, int64, error) {
		s := New(P)
		rng := rand.New(rand.NewPCG(290, 1))
		next := make([]int, P)
		naive := map[string]int64{}
		seen := map[[2]int]bool{}
		maxOff := make([]int64, P)
		for i := range maxOff {
			maxOff[i] = -1
		}
		for st := 0; st < steps; st++ {
			if rng.IntN(4) == 0 && withRestarts { // 重启只夹在批边界；两种运行保持同一 RNG 消耗序列
				s.Restart()
			}
			var batch []wm.Rec
			for _, p := range rng.Perm(P)[:1+rng.IntN(P)] {
				if next[p] >= L {
					continue
				}
				start := next[p] - rng.IntN(next[p]+1) // 连续片段起点不晚于首条未生效位点
				end := start + 1 + rng.IntN(L-start)
				for off := start; off < end; off++ {
					batch = append(batch, wm.Rec{Partition: p, Offset: int64(off), Key: keyOf(p, off), Val: valOf(p, off)})
					if !seen[[2]int{p, off}] { // 朴素参照：不同 (分区,位点) 各计一次
						seen[[2]int{p, off}] = true
						naive[keyOf(p, off)] += valOf(p, off)
						maxOff[p] = int64(off)
					}
				}
				if end > next[p] {
					next[p] = end
				}
			}
			if len(batch) == 0 {
				continue
			}
			if err := s.Write(batch); err != nil {
				return nil, nil, 0, err
			}
		}
		marks := make([]int64, P)
		for p := range marks {
			marks[p] = s.Watermark(p)
			if marks[p] != maxOff[p] { // 不变量2：分区独立，水位=本分区已生效最大位点
				return nil, nil, 0, fmt.Errorf("invariant 2: W[%d]=%d want %d", p, marks[p], maxOff[p])
			}
		}
		if !maps.Equal(s.Table(), naive) { // 不变量1：与朴素参照一致
			return nil, nil, 0, fmt.Errorf("invariant 1: table=%v want %v", s.Table(), naive)
		}
		return s.Table(), marks, s.Duplicates(), nil
	}

	t0, m0, d0, err := run(false)
	if err != nil {
		return err
	}
	t1, m1, d1, err := run(true)
	if err != nil {
		return err
	}
	if !maps.Equal(t0, t1) || !equalI64(m0, m1) || d0 != d1 { // 不变量3：重启无关
		return fmt.Errorf("invariant 3: without-restart %v/%v/%d != with-restart %v/%v/%d", t0, m0, d0, t1, m1, d1)
	}
	return rejectLeavesNoTrace() // 不变量4
}

func equalI64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rejectLeavesNoTrace 核验三类可判定错误互不相同、优先级正确，且被拒批整体不留痕。
func rejectLeavesNoTrace() error {
	s := New(2)
	if err := s.Write([]wm.Rec{{Partition: 0, Offset: 0, Key: "a", Val: 1}}); err != nil {
		return err
	}
	snap := s.Table()
	bad := []struct {
		batch []wm.Rec
		want  error
	}{
		{[]wm.Rec{{Partition: -1, Offset: -1, Key: "", Val: 1}, {Partition: 0, Offset: 0, Key: "a", Val: 1}}, wm.ErrIllegalRec}, // 非法优先于乱序
		{[]wm.Rec{{Partition: 0, Offset: 5, Key: "a", Val: 1}, {Partition: 0, Offset: 5, Key: "a", Val: 1}}, wm.ErrOutOfOrder},
		{[]wm.Rec{{Partition: 1, Offset: 0, Key: "b", Val: 1}, {Partition: 2, Offset: 0, Key: "c", Val: 1}}, sink.ErrTooManyPartitions},
	}
	for i, c := range bad {
		err := s.Write(c.batch)
		if !errors.Is(err, c.want) {
			return fmt.Errorf("invariant 4 case %d: err=%v want %v", i, err, c.want)
		}
		if !maps.Equal(s.Table(), snap) || s.Watermark(1) != -1 || s.Duplicates() != 0 {
			return fmt.Errorf("invariant 4 case %d: rejected batch changed state", i)
		}
	}
	return nil
}
