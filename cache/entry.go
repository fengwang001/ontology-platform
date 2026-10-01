package cache

import "strings"

// entry 是缓存内部条目。last 全局互不相同。
type entry struct {
	key      string
	manifest []Pair
	mkey     string // 清单的规范化编码，用于逐对完全相同的判定
	result   string
	last     uint64
	alive    bool
}

// keyGroup 保存同一动作键下的全部条目。
type keyGroup struct {
	entries []*entry
	byMKey  map[string]*entry
}

// manifestKey 将清单编码为可比较的规范形式（长度前缀避免歧义）。
func manifestKey(manifest []Pair) string {
	var b strings.Builder
	for _, p := range manifest {
		writeLenPrefixed(&b, p.Path)
		writeLenPrefixed(&b, p.Digest)
	}
	return b.String()
}

func writeLenPrefixed(b *strings.Builder, s string) {
	var num [20]byte
	i := len(num)
	for n := len(s); ; n /= 10 {
		i--
		num[i] = byte('0' + n%10)
		if n < 10 {
			break
		}
	}
	b.Write(num[i:])
	b.WriteByte(':')
	b.WriteString(s)
	b.WriteByte(';')
}

// heapItem 是全局淘汰堆中的一项。条目 last 刷新时会压入新项，
// 旧项因 last 与条目当前值不一致而在弹出时被丢弃（惰性删除）。
type heapItem struct {
	last uint64
	e    *entry
}

// entryHeap 是按 last 升序的最小堆，用于全局淘汰。
type entryHeap []heapItem

func (h entryHeap) Len() int { return len(h) }

func (h entryHeap) Less(i, j int) bool { return h[i].last < h[j].last }

func (h entryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *entryHeap) Push(x any) { *h = append(*h, x.(heapItem)) }

func (h *entryHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
