// Package sink 持有持久区（结果表 + 各分区水位 + 重复数），
// 每批在副本上逐条生效、整体替换，实现原子提交与重启重建。依赖 wm。
package sink

import (
	"errors"
	"maps"
	"sync"

	"ontology/wm"
)

// ErrTooManyPartitions：已有水位分区数加本批新分区数超过上限。
var ErrTooManyPartitions = errors.New("sink: partition count exceeds maxPartitions")

type state struct {
	table map[string]int64 // Key -> 累加值
	marks map[int]int64    // 分区 -> 已生效记录最大位点
	dups  int64            // 累计重复数
}

// Sink 并发安全；mu 串行化整批提交与读快照。
type Sink struct {
	mu sync.Mutex
	d  state
	// rebuildScanned 非导出：最近一次 Restart 重建时检查过的条目个数。
	rebuildScanned int
}

func New() *Sink {
	return &Sink{d: state{table: map[string]int64{}, marks: map[int]int64{}}}
}

// Commit 整批原子提交：任一校验不过则持久区完全不变。
func (s *Sink) Commit(batch []wm.Rec, maxPartitions int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := wm.Validate(batch); err != nil { // 记录非法 → 乱序，wm 内按此优先级
		return err
	}
	fresh := map[int]struct{}{} // 本批新出现（尚无水位）的分区
	for i := range batch {
		if _, ok := s.d.marks[batch[i].Partition]; !ok {
			fresh[batch[i].Partition] = struct{}{}
		}
	}
	if len(s.d.marks)+len(fresh) > maxPartitions {
		return ErrTooManyPartitions
	}
	nt, nm := maps.Clone(s.d.table), maps.Clone(s.d.marks) // 在副本上工作
	nd := s.d.dups
	for i := range batch {
		r := batch[i]
		w := int64(-1) // 从未生效过记录的分区水位为 -1
		if x, ok := nm[r.Partition]; ok {
			w = x
		}
		if dup, next := wm.Apply(w, r); dup {
			nd++
		} else {
			nt[r.Key] += r.Val
			nm[r.Partition] = next // 只推进记录所属分区
		}
	}
	s.d = state{table: nt, marks: nm, dups: nd} // 整体替换 => 失败不留痕
	return nil
}

// Restart 模拟崩溃：逐项从持久区读回重建，不重放任何记录。
func (s *Sink) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	nt := make(map[string]int64, len(s.d.table))
	for k, v := range s.d.table {
		nt[k] = v
		n++ // 检查一个表条目
	}
	nm := make(map[int]int64, len(s.d.marks))
	for p, w := range s.d.marks {
		nm[p] = w // 水位直接读回，不重新计算
		n++       // 检查一个分区水位条目
	}
	s.d = state{table: nt, marks: nm, dups: s.d.dups}
	s.rebuildScanned = n
}

func (s *Sink) snapshot() state {
	s.mu.Lock()
	defer s.mu.Unlock()
	return state{table: maps.Clone(s.d.table), marks: maps.Clone(s.d.marks), dups: s.d.dups}
}

func (s *Sink) Table() map[string]int64 { return s.snapshot().table }

func (s *Sink) Watermark(p int) int64 {
	if w, ok := s.snapshot().marks[p]; ok {
		return w
	}
	return -1
}

func (s *Sink) Duplicates() int64 { return s.snapshot().dups }

// RebuildBoundOK 只暴露布尔结论：4 分区 3 Key 多档 m 下重启检查数有界且不随 m 变。
func RebuildBoundOK(ms []int) bool {
	const P, K, slack = 4, 3, 2
	want := -1
	for _, m := range ms {
		s := New()
		next := make([]int64, P)
		for n := 0; n < m; {
			b := make([]wm.Rec, 0, 64)
			for len(b) < 64 && n < m {
				p := n % P
				b = append(b, wm.Rec{Partition: p, Offset: next[p], Key: string(rune('x' + n%K)), Val: 1})
				next[p]++
				n++
			}
			if s.Commit(b, P) != nil {
				return false
			}
		}
		s.Restart()
		if s.rebuildScanned > P+K+slack || (want >= 0 && s.rebuildScanned != want) {
			return false
		}
		want = s.rebuildScanned
	}
	return true
}
