// Package arc 单分区归档列表：追加 (off, ts)、按 ts 从头部驱逐、求幸存最大位点。
// 不依赖其他包。条目按 ts 单调递增追加（单调性由调用方保证），
// 因此驱逐只需从头部逐条检查到第一个不满足条件的条目即停。
package arc

// Entry 一条归档记录：已提交位点与其时间戳。
type Entry struct {
	Off int64
	Ts  int64
}

// Archive 单分区归档。
type Archive struct {
	entries []Entry
	checked int // 最近一次 Evict 检查过的条目数；非导出，仅供包内白盒测试观测
}

// Append 追加一条 (off, ts)。
func (a *Archive) Append(off, ts int64) {
	a.entries = append(a.entries, Entry{Off: off, Ts: ts})
}

// Evict 驱逐 ts < cutoff（严格小于）的条目。
// 利用 ts 单调有序，从头部逐条检查，遇到第一个 ts >= cutoff 的条目即停，
// 检查条数不超过「驱逐条数 + 1」，绝不整表扫描。
func (a *Archive) Evict(cutoff int64) {
	a.checked = 0
	i := 0
	for i < len(a.entries) {
		a.checked++
		if a.entries[i].Ts >= cutoff {
			break
		}
		i++
	}
	a.entries = a.entries[i:]
}

// Max 返回幸存条目中的最大位点；空归档返回 ok=false。
// 位点单调递增，最大位点即末条，O(1)。
func (a *Archive) Max() (off int64, ok bool) {
	if len(a.entries) == 0 {
		return 0, false
	}
	return a.entries[len(a.entries)-1].Off, true
}

// Len 返回幸存条目数。
func (a *Archive) Len() int { return len(a.entries) }
