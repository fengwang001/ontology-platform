package cookie

import (
	"container/heap"
	"fmt"
)

// heapPopForTest 仅供性能测试测量一次 LRU 栈顶弹出的比较次数。
func heapPopForTest(h *lruHeap) *entry {
	return heap.Pop(h).(*entry)
}

// debugCheckConsistency 校验内核堆/索引计数与 byKey 完全一致。
func (k *Kernel) debugCheckConsistency() error {
	total := 0
	for domain, s := range k.sites {
		n := len(s.byKey)
		total += n
		if s.count.count != n {
			return fmt.Errorf("domain %s count=%d but byKey=%d", domain, s.count.count, n)
		}
		if s.lru.Len() != n {
			return fmt.Errorf("domain %s lru=%d but byKey=%d", domain, s.lru.Len(), n)
		}
		exp := 0
		for _, e := range s.byKey {
			if e.expires != nil {
				exp++
			}
		}
		if s.expiry.Len() != exp {
			return fmt.Errorf("domain %s expiry heap=%d expected=%d", domain, s.expiry.Len(), exp)
		}
		if s.count.count > k.cfg.PerSiteLimit {
			return fmt.Errorf("per-site limit exceeded: %s %d > %d",
				domain, s.count.count, k.cfg.PerSiteLimit)
		}
	}
	if k.entries != total {
		return fmt.Errorf("kernel entries=%d but byKey total=%d", k.entries, total)
	}
	if k.siteCnt.Len() != len(k.sites) {
		return fmt.Errorf("siteCnt=%d sites=%d", k.siteCnt.Len(), len(k.sites))
	}
	if total > k.cfg.GlobalLimit {
		return fmt.Errorf("global limit exceeded: %d > %d", total, k.cfg.GlobalLimit)
	}
	return nil
}
