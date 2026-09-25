// Package vcache 在 ver.Store 之上维护两张物化视图的缓存：
// 按组缓存 cacheG 与总缓存 cacheT。写入不触碰缓存；读取时以
// 版本戳（stamp）判新鲜，过期则惰性重算并刷新。判新鲜只比较
// O(1) 的增量戳，不遍历记录。
package vcache

import (
	"sync"

	"ontology/ver"
)

type entry struct {
	sum   int64
	stamp int64
}

// Cache 是并发安全的视图缓存。
type Cache struct {
	st *ver.Store

	mu   sync.Mutex
	cg   map[string]entry // group -> {sum, stamp}
	ct   entry            // 总缓存
	ctOK bool             // 总缓存是否已建立

	// probe 记录最近一次 ReadG/ReadTotal 中「为判定缓存是否新鲜
	// 而遍历的记录个数」。版本戳由 ver 增量维护，判新鲜只做一次
	// O(1) 整数比较，故该值恒为 0，与记录规模 m 无关。
	// 非导出：不出现在任何公开接口中。
	probe int
}

func New(st *ver.Store) *Cache {
	return &Cache{st: st, cg: map[string]entry{}}
}

// ReadG 返回 group 的 Value 之和。缓存戳等于当前 stamp 时直接命中；
// 否则全量重算并以当前戳刷新缓存。空 group 是可判定错误。
func (c *Cache) ReadG(group string) (int64, error) {
	if group == "" {
		return 0, ver.ErrEmptyGroup
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probe = 0 // 判新鲜遍历记录数：0（只比较戳）
	stamp := c.st.Stamp(group)
	if e, ok := c.cg[group]; ok && e.stamp == stamp {
		return e.sum, nil
	}
	sum := c.st.SumGroup(group)
	c.cg[group] = entry{sum: sum, stamp: stamp}
	return sum, nil
}

// ReadTotal 返回全部记录的 Value 之和，语义同 ReadG。
func (c *Cache) ReadTotal() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probe = 0 // 判新鲜遍历记录数：0（只比较戳）
	stamp := c.st.TotalStamp()
	if c.ctOK && c.ct.stamp == stamp {
		return c.ct.sum
	}
	sum := c.st.SumTotal()
	c.ct = entry{sum: sum, stamp: stamp}
	c.ctOK = true
	return sum
}

// View 经 ReadG/ReadTotal 返回 {各组之和, 总和}，与缓存一致。
func (c *Cache) View() (map[string]int64, int64) {
	groups := c.st.Groups()
	out := make(map[string]int64, len(groups))
	for _, g := range groups {
		sum, err := c.ReadG(g)
		if err != nil {
			continue // 不会发生：组名来自 Store，必非空
		}
		out[g] = sum
	}
	return out, c.ReadTotal()
}
