package aria

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// addOv 返回 a+b 及是否溢出 int64。
func addOv(a, b int64) (int64, bool) {
	s := a + b
	if a > 0 && b > 0 && s < 0 || a < 0 && b < 0 && s > 0 {
		return 0, true
	}
	return s, false
}

// trial 是一次试执行/重执行的结果。
type trial struct {
	acc    int64
	buf    map[int]int64 // 写缓冲：WS 及其缓冲值
	rs     []int         // 读集：取自执行所用状态而非缓冲的键，升序去重
	ws     []int         // 写集：缓冲的键，升序去重
	failed bool          // 任一步 acc 或缓冲值溢出 int64
}

// execute 在给定状态上执行一个事务，状态不被修改。
func execute(state []int64, ops []Op) trial {
	t := trial{buf: make(map[int]int64)}
	rsSeen := make(map[int]struct{})
	for _, op := range ops {
		switch op.Kind {
		case OpRead:
			v, ok := t.buf[op.Key]
			if !ok {
				// 取自执行所用状态而非缓冲，计入读集。
				v = state[op.Key]
				if _, seen := rsSeen[op.Key]; !seen {
					rsSeen[op.Key] = struct{}{}
					t.rs = append(t.rs, op.Key)
				}
			}
			acc, ov := addOv(t.acc, v)
			if ov {
				t.failed = true
				return t
			}
			t.acc = acc
		case OpWrite:
			nv, ov := addOv(t.acc, op.D)
			if ov {
				t.failed = true
				return t
			}
			t.buf[op.Key] = nv
		}
	}
	t.ws = make([]int, 0, len(t.buf))
	for k := range t.buf {
		t.ws = append(t.ws, k)
	}
	sort.Ints(t.ws)
	sort.Ints(t.rs)
	return t
}

// runBatchLocked 在持有 e.mu 的前提下执行一个批次，调用方保证有待处理事务。
func (e *Executor) runBatchLocked() []Result {
	n := len(e.pending)
	if n > e.bsz {
		n = e.bsz
	}
	batch := e.pending[:n]
	e.pending = append([]tx(nil), e.pending[n:]...)

	// ① 并行阶段：每个事务都在批起点快照上试执行。
	snap := make([]int64, len(e.state))
	copy(snap, e.state)
	trials := make([]trial, n)
	var wg sync.WaitGroup
	for i := range batch {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			trials[i] = execute(snap, batch[i].ops)
		}(i)
	}
	wg.Wait()

	// ② 对未失败者按事务号升序建预留表（批内即升序，先填者即最小）。
	// 中止者也计入预留表；溢出失败者不参与预留。
	wres := make(map[int]uint64, n)
	rres := make(map[int]uint64, n)
	for i := range batch {
		if trials[i].failed {
			continue
		}
		for _, k := range trials[i].ws {
			if _, ok := wres[k]; !ok {
				wres[k] = batch[i].id
			}
		}
		for _, k := range trials[i].rs {
			if _, ok := rres[k]; !ok {
				rres[k] = batch[i].id
			}
		}
	}
	// 建立与判定只触及 wres/rres 中实际存在的表项（未命中不占用表项），
	// 故触及的不同表项数 = len(wres)+len(rres) ≤ 批内操作总数 ≤ 其两倍。
	e.lastTouches = len(wres) + len(rres)

	// 判定：t 提交当且仅当无 WAW 且不同时有 RAW 与 WAR。
	results := make([]Result, n)
	committed := make([]bool, n)
	for i := range batch {
		id := batch[i].id
		if trials[i].failed {
			results[i] = Result{TxID: id, Phase: PhaseFailed,
				Reason: "并行阶段溢出失败，不参与预留，不回退"}
			continue
		}
		var waw, raw, war bool
		var wawK, rawK, warK = -1, -1, -1
		for _, k := range trials[i].rs {
			if w, ok := wres[k]; ok && w < id {
				raw, rawK = true, k
				break
			}
		}
		for _, k := range trials[i].ws {
			if w, ok := wres[k]; ok && w < id && !waw {
				waw, wawK = true, k
			}
			if r, ok := rres[k]; ok && r < id && !war {
				war, warK = true, k
			}
		}
		committed[i] = !waw && !(raw && war)
		results[i] = Result{TxID: id, Reason: depReason(id, waw, wawK, raw, rawK, war, warK, wres, rres)}
	}

	// ③ 提交者的缓冲按事务号升序写入状态（其写集两两不交）。
	for i := range batch {
		if !committed[i] {
			continue
		}
		for k, v := range trials[i].buf {
			e.state[k] = v
		}
		results[i].Phase = PhaseParallel
		results[i].Acc = trials[i].acc
		results[i].HasAcc = true
	}

	// 其余按事务号升序在当前状态上串行重新执行并立即写入。
	for i := range batch {
		if committed[i] || trials[i].failed {
			continue
		}
		tr := execute(e.state, batch[i].ops)
		if tr.failed {
			results[i].Phase = PhaseFailed
			results[i].HasAcc = false
			results[i].Reason += "；回退重执行溢出失败"
			continue
		}
		for k, v := range tr.buf {
			e.state[k] = v
		}
		results[i].Phase = PhaseFallback
		results[i].Acc = tr.acc
		results[i].HasAcc = true
	}
	return results
}

// depReason 生成判定依据文本，供日志与测试核对。
func depReason(id uint64, waw bool, wawK int, raw bool, rawK int, war bool, warK int, wres, rres map[int]uint64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wres=%v rres=%v", wres, rres)
	if waw {
		fmt.Fprintf(&b, "；WAW:键%d wres=%d<%d", wawK, wres[wawK], id)
	}
	if raw {
		fmt.Fprintf(&b, "；RAW:键%d wres=%d<%d", rawK, wres[rawK], id)
	}
	if war {
		fmt.Fprintf(&b, "；WAR:键%d rres=%d<%d", warK, rres[warK], id)
	}
	if !waw && !raw && !war {
		b.WriteString("；无依赖")
	}
	if !waw && !(raw && war) {
		b.WriteString("；并行提交")
	} else {
		b.WriteString("；中止回退")
	}
	return b.String()
}
