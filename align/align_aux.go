package align

import (
	"maps"
	"math"
	"math/rand"

	"ontology/chanbuf"
)

// undo 记录一次状态变更，批处理失败时按逆序回放；字段按 kind 复用。
type undo struct {
	kind    uint8
	ch      int
	k       string
	n, v    int64
	it      chanbuf.Item
	pb      bool
}

const (
	uSeq uint8 = iota + 1 // 撤销：seq--
	uSum                  // sum[k]-=v
	uOut                  // out 截断到 n
	uSnap                 // 删除快照 n（每号屏障只对齐一次，无需恢复旧值）
	uBuf                  // 通道 ch 缓冲裁掉队尾
	uPop                  // 通道 ch 把 (it,n=seq) 插回队首
	uBlock                // 通道 ch 阻塞态恢复为 pb
	uProc                 // lastProc[ch] 恢复为 v
	uMark                 // 撤销一次 MarkBarrier，阻塞态恢复为 pb
)

// rollback 逆序撤销本批全部变更，保证失败不留痕。
func (a *Aligner) rollback(log []undo) {
	for i := len(log) - 1; i >= 0; i-- {
		u := log[i]
		switch u.kind {
		case uSeq:
			a.seq--
		case uSum:
			if a.sum[u.k] -= u.v; a.sum[u.k] == 0 {
				delete(a.sum, u.k)
			}
		case uOut:
			a.out = a.out[:u.n]
		case uSnap:
			delete(a.snaps, u.n)
		case uBuf:
			a.ch[u.ch].Trim(a.ch[u.ch].Len() - 1)
		case uPop:
			a.ch[u.ch].PushFront(u.it, u.n)
		case uBlock:
			setBlock(a.ch[u.ch], u.pb)
		case uProc:
			a.lastProc[u.ch] = u.v
		case uMark:
			a.ch[u.ch].RestoreMark(u.pb)
		}
	}
}

func setBlock(s *chanbuf.State, b bool) {
	if b {
		s.Block()
	} else {
		s.Unblock()
	}
}

// SelfCheck 用内置输入序列核验第二节四条不变量，全部成立返回 true。
// 不读写接收方状态（内部自建实例），可安全并发调用。
func (a *Aligner) SelfCheck() bool { return selfRandom() && selfAtomic() }

func mkRec(c int, k string, v int64) chanbuf.Item {
	return chanbuf.Item{Ch: c, Kind: chanbuf.KindRecord, Key: k, Val: v}
}
func mkBar(c int, n int64) chanbuf.Item {
	return chanbuf.Item{Ch: c, Kind: chanbuf.KindBarrier, ID: n}
}
func keyOf(tag int64) string { return string(rune('a' + tag%2)) }

// selfRandom：随机交错（含连续多个屏障）下核验不变量 1、2、3。
// 记录 Val 取全局到达位置作唯一 tag，Key 由 tag 奇偶决定，凭 tag 核对。
func selfRandom() bool {
	a := New(1 << 30)
	rng := rand.New(rand.NewSource(1))
	var arr, bpos [2][]int64 // 各通道记录 tag 序列、屏障 n 的到达位置（下标 n-1）
	for pos := int64(1); pos <= 2000; pos++ {
		c := rng.Intn(2)
		it := mkRec(c, keyOf(pos), pos)
		if rng.Intn(3) == 0 {
			it = mkBar(c, a.ch[c].Barriers()+1)
			bpos[c] = append(bpos[c], pos)
		} else {
			arr[c] = append(arr[c], pos)
		}
		if _, err := a.Push([]chanbuf.Item{it}); err != nil {
			return false
		}
	}
	drain(a)
	var idx [2]int
	var seen []int64
	nOut := 0
	for _, o := range a.out {
		if o.Kind == chanbuf.KindBarrier { // 不变量1、2：快照与屏障前记录都等于切割
			sum, tags := cut(o.ID, bpos, arr)
			if !maps.Equal(a.snaps[o.ID], sum) || !inTags(seen, tags) {
				return false
			}
			continue
		}
		if arr[o.Ch][idx[o.Ch]] != o.Val { // 不变量2：通道内保序
			return false
		}
		idx[o.Ch]++
		nOut++
		seen = append(seen, o.Val)
	}
	bufRec := 0 // 缓冲中的记录 = 缓冲元素 - 缓冲中未处理的屏障
	for c := 0; c < 2; c++ {
		bufRec += a.ch[c].Len() - int(a.ch[c].Barriers()-a.lastProc[c])
	}
	if bufRec != 0 || nOut != len(arr[0])+len(arr[1]) { // 不变量3：不丢不重
		return false
	}
	total, _ := cut(math.MaxInt64, bpos, arr)
	return maps.Equal(a.sum, total)
}

// selfAtomic：不变量4 —— 三类拒绝互不相同、整批不留痕、之后仍可使用。
func selfAtomic() bool {
	if ErrInvalidElement == ErrBarrierOrder || ErrBarrierOrder == ErrBufferLimit ||
		ErrBufferLimit == ErrInvalidElement {
		return false
	}
	bad := []chanbuf.Item{mkRec(7, "a", 1), mkRec(0, "", 1), mkBar(0, 3)}
	wantErr := []error{ErrInvalidElement, ErrInvalidElement, ErrBarrierOrder}
	for i := range bad {
		a := New(10)
		if _, err := a.Push(bad[i:i+1]); err != wantErr[i] ||
			len(a.sum) != 0 || a.ch[0].Len() != 0 || a.seq != 0 {
			return false
		}
	}
	a := New(1)
	if _, err := a.Push([]chanbuf.Item{mkBar(0, 1)}); err != nil {
		return false
	}
	if _, err := a.Push([]chanbuf.Item{mkRec(0, "q", 1), mkRec(0, "r", 1)}); err != ErrBufferLimit {
		return false
	}
	if len(a.sum) != 0 || a.ch[0].Len() != 0 { // 整批不留痕
		return false
	}
	if _, err := a.Push([]chanbuf.Item{mkBar(1, 1)}); err != nil { // 之后仍正常
		return false
	}
	_, ok := a.snaps[1]
	return ok
}

// cut 计算切割参照：两通道各自屏障 n 之前的记录之和与 tag 集合。
func cut(n int64, bpos, arr [2][]int64) (map[string]int64, map[int64]bool) {
	sum, tags := map[string]int64{}, map[int64]bool{}
	for c := 0; c < 2; c++ {
		lim := int64(math.MaxInt64)
		if n <= int64(len(bpos[c])) {
			lim = bpos[c][n-1]
		}
		for _, t := range arr[c] {
			if t < lim {
				sum[keyOf(t)] += t
				tags[t] = true
			}
		}
	}
	return sum, tags
}

// inTags 核验 seen 与 tags 是同一集合（seen 本身无重复）。
func inTags(seen []int64, tags map[int64]bool) bool {
	if len(seen) != len(tags) {
		return false
	}
	for _, t := range seen {
		if !tags[t] {
			return false
		}
	}
	return true
}

// drain 给落后通道补屏障直到两通道屏障数相同；此后缓冲必被排干。
func drain(a *Aligner) {
	for a.ch[0].Barriers() != a.ch[1].Barriers() {
		c := 0
		if a.ch[1].Barriers() < a.ch[0].Barriers() {
			c = 1
		}
		if _, err := a.Push([]chanbuf.Item{mkBar(c, a.ch[c].Barriers()+1)}); err != nil {
			panic(err)
		}
	}
}
