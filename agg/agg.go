// Package agg 单 key 聚合：Seq→Val 集合、sum、纠正窗口（首次到达序 FIFO）
// 与四类事件判定。不依赖其他包。
package agg

// Kind 事件类别。
type Kind int

const (
	KindNew        Kind = iota // 新 Seq
	KindLate                   // 迟到新 Seq（seq 小于当前最大 Seq，照常接受）
	KindCorrection             // 窗口内纠正：撤回旧值改新值
	KindDuplicate              // 已见 Seq 且同值：幂等 no-op
	KindStale                  // 过期纠正：Seq 已出窗口，拒绝
)

// Result 一次 Apply 的结果；OldSum/NewSum 为本事件前后的 sum。
type Result struct {
	Kind   Kind
	OldSum int64
	NewSum int64
}

// Key 单 key 的聚合状态。调用方保证 seq > 0。
type Key struct {
	w      int
	vals   map[int64]int64 // 不同 Seq → 当前 Val
	sum    int64
	maxSeq int64
	window []int64 // 按首次到达序，容量 w
	inWin  map[int64]struct{}
	checks int // 最近一次 Apply 为判定窗口成员检查过的条目数（非导出，仅供包内测试）
}

// NewKey 创建一个窗口容量为 w 的聚合器。
func NewKey(w int) *Key {
	return &Key{w: w, vals: map[int64]int64{}, inWin: map[int64]struct{}{}}
}

// Sum 返回当前运行和。
func (k *Key) Sum() int64 { return k.sum }

// Apply 处理一条事件，返回类别与前后 sum。
func (k *Key) Apply(seq, val int64) Result {
	k.checks = 0
	old := k.sum
	if cur, seen := k.vals[seq]; seen {
		if cur == val {
			return Result{Kind: KindDuplicate, OldSum: old, NewSum: old} // 幂等，不动任何状态
		}
		k.checks = 1 // 映射定位窗口成员，O(1)，与窗口长度无关
		if _, ok := k.inWin[seq]; !ok {
			return Result{Kind: KindStale, OldSum: old, NewSum: old} // 合法拒绝，不动状态
		}
		k.vals[seq] = val // 纠正不改首次到达顺序，窗口不动
		k.sum += val - cur
		return Result{Kind: KindCorrection, OldSum: old, NewSum: k.sum}
	}
	kind := KindNew
	if seq < k.maxSeq {
		kind = KindLate
	}
	if seq > k.maxSeq {
		k.maxSeq = seq
	}
	k.vals[seq] = val
	k.sum += val
	k.window = append(k.window, seq)
	k.inWin[seq] = struct{}{}
	if len(k.window) > k.w { // 淘汰最早到达者
		delete(k.inWin, k.window[0])
		k.window = k.window[1:]
	}
	return Result{Kind: kind, OldSum: old, NewSum: k.sum}
}
