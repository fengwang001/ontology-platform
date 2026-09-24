// Package align 实现两个输入通道的检查点屏障对齐、求和状态、快照与重放。
// 依赖方向：align -> chanbuf。Aligner 自带读写锁，只读方法可并发调用。
package align

import (
	"errors"
	"maps"
	"sync"

	"ontology/chanbuf"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrInvalidElement = errors.New("align: bad channel or empty key")
	ErrBarrierOrder   = errors.New("align: barrier id out of order")
	ErrBufferLimit    = errors.New("align: buffered elements exceed maxBuffered")
)

// Aligner 是两通道对齐算子的全部状态。lastCheck 记录最近处理单个输入元素
// 时检查过的缓冲元素个数（重放每弹出一个计 1）；非导出，不进公开接口。
type Aligner struct {
	mu        sync.RWMutex
	ch        [2]*chanbuf.State
	sum       map[string]int64
	snaps     map[int64]map[string]int64
	out       []chanbuf.Out
	seq       int64
	lastProc  [2]int64
	maxBuf    int
	lastCheck int
}

// New 创建算子，maxBuffered 为两通道缓冲元素总数上限。
func New(maxBuffered int) *Aligner {
	return &Aligner{ch: [2]*chanbuf.State{chanbuf.New(), chanbuf.New()},
		sum: map[string]int64{}, snaps: map[int64]map[string]int64{}, maxBuf: maxBuffered}
}

// Push 按顺序处理一批元素，返回本批输出片段；任一被拒则凭撤销日志整体回滚。
func (a *Aligner) Push(items []chanbuf.Item) ([]chanbuf.Out, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	start := len(a.out)
	log := make([]undo, 0, len(items)+4)
	for i := range items {
		if err := a.step(items[i], &log); err != nil {
			a.rollback(log)
			return nil, err
		}
	}
	return append([]chanbuf.Out(nil), a.out[start:]...), nil
}

// step 处理单个元素；所有拒绝性校验都在任何状态变更之前完成。
func (a *Aligner) step(it chanbuf.Item, log *[]undo) error {
	a.lastCheck = 0
	bad := it.Ch != 0 && it.Ch != 1
	bad = bad || it.Kind != chanbuf.KindRecord && it.Kind != chanbuf.KindBarrier
	bad = bad || it.Kind == chanbuf.KindRecord && it.Key == ""
	if bad {
		return ErrInvalidElement
	}
	s := a.ch[it.Ch]
	if it.Kind == chanbuf.KindBarrier && it.ID != s.Barriers()+1 {
		return ErrBarrierOrder
	}
	a.seq++
	*log = append(*log, undo{kind: uSeq})
	was := s.Blocked()
	if it.Kind == chanbuf.KindBarrier {
		s.MarkBarrier() // 屏障按到达计数，含进入缓冲的屏障
		*log = append(*log, undo{kind: uMark, ch: it.Ch, pb: was})
	}
	if was { // 到达时已阻塞：记录与屏障一律按序入缓冲，不处理
		s.Push(it, a.seq)
		*log = append(*log, undo{kind: uBuf, ch: it.Ch})
		if a.ch[0].Len()+a.ch[1].Len() > a.maxBuf {
			return ErrBufferLimit
		}
		return nil
	}
	if it.Kind == chanbuf.KindRecord {
		a.record(it, log)
		return nil
	}
	a.setProc(it.Ch, it.ID, log)
	if a.lastProc[1-it.Ch] == it.ID {
		a.align(it.ID, log) // 第二个屏障 n 被处理的那一刻：对齐
	}
	return nil
}

// record 处理一条未阻塞记录：累加并原样进入输出流。
func (a *Aligner) record(it chanbuf.Item, log *[]undo) {
	if a.sum[it.Key] += it.Val; a.sum[it.Key] == 0 {
		delete(a.sum, it.Key)
	}
	*log = append(*log, undo{kind: uSum, k: it.Key, v: it.Val})
	a.out = append(a.out, it)
	*log = append(*log, undo{kind: uOut, n: int64(len(a.out) - 1)})
}

// align 严格按序：拷贝快照 n → 转发屏障 n → 两通道解阻塞 → 按到达顺序重放。
func (a *Aligner) align(n int64, log *[]undo) {
	a.snaps[n] = maps.Clone(a.sum)
	*log = append(*log, undo{kind: uSnap, n: n})
	a.out = append(a.out, chanbuf.Out{Ch: -1, Kind: chanbuf.KindBarrier, ID: n})
	*log = append(*log, undo{kind: uOut, n: int64(len(a.out) - 1)})
	for c := 0; c < 2; c++ {
		a.ch[c].Unblock()
		*log = append(*log, undo{kind: uBlock, ch: c, pb: true})
	}
	a.replay(log)
}

// replay 用全局到达序号 O(1) 归并两通道队首逐个重处理；重放到本通道屏障
// 则重新阻塞（其后缓冲留下），两通道再次凑齐同编号则嵌套对齐。
func (a *Aligner) replay(log *[]undo) {
	for {
		best, bseq := -1, int64(0)
		for c := 0; c < 2; c++ {
			if a.ch[c].Blocked() {
				continue
			}
			if sq, ok := a.ch[c].HeadSeq(); ok && (best == -1 || sq < bseq) {
				best, bseq = c, sq
			}
		}
		if best == -1 {
			return
		}
		it, seq, _ := a.ch[best].Pop()
		a.lastCheck++
		*log = append(*log, undo{kind: uPop, ch: best, it: it, n: seq})
		if it.Kind == chanbuf.KindRecord {
			a.record(it, log)
			continue
		}
		a.ch[best].Block() // 计数在到达时已加，这里只重新阻塞
		*log = append(*log, undo{kind: uBlock, ch: best})
		a.setProc(best, it.ID, log)
		if a.lastProc[1-best] == it.ID {
			a.align(it.ID, log)
		}
	}
}

func (a *Aligner) setProc(c int, n int64, log *[]undo) {
	*log = append(*log, undo{kind: uProc, ch: c, v: a.lastProc[c]})
	a.lastProc[c] = n
}

// Snapshot 返回检查点 n 的快照拷贝；不存在时 ok=false。
func (a *Aligner) Snapshot(n int64) (map[string]int64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.snaps[n]
	return maps.Clone(s), ok
}

// State 返回当前 sum 的拷贝（等于输出流中全部记录按 Key 求和）。
func (a *Aligner) State() map[string]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return maps.Clone(a.sum)
}
