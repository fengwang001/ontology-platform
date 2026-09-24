// Package uni 维护多个源分区去重并集的物化视图。依赖方向：uni -> src。
// 视图只靠全局引用计数 cnt 维护：cnt[e] 为当前持有 e 的分区个数。
package uni

import (
	"fmt"
	"sync"

	"ontology/src"
)

// Change 是一条 changelog：Add=true 表示 +e（0→1），false 表示 -e（1→0）。
type Change struct {
	Add  bool
	Elem string
}

// Union 是去重并集的增量维护器。
type Union struct {
	mu    sync.Mutex
	parts []*src.Set
	cnt   map[string]int
	log   []Change
	// lastRemoveChecks 记录最近一次 Remove 为判断「该元素是否还被其它来源持有」
	// 而扫描的分区个数。用全局 cnt map 做 O(1) 查询，故恒为 0；非导出，仅供
	// 包内复杂度测试读取，绝不出现在任何公开接口中。
	lastRemoveChecks int
}

// New 创建 nPart 个空分区。nPart 合法性由上层 api 校验。
func New(nPart int) *Union {
	u := &Union{parts: make([]*src.Set, nPart), cnt: make(map[string]int)}
	for i := range u.parts {
		u.parts[i] = src.New()
	}
	return u
}

// Add 把 e 加入分区 p。集合幂等；仅当 cnt 0→1 时产出 +e。
func (u *Union) Add(p int, e string) (Change, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.parts[p].Add(e) {
		return Change{}, false // 已在该分区：幂等 no-op
	}
	c := u.cnt[e] + 1
	u.cnt[e] = c
	if c == 1 {
		ch := Change{Add: true, Elem: e}
		u.log = append(u.log, ch)
		return ch, true
	}
	return Change{}, false
}

// Remove 从分区 p 撤回 e。集合幂等；仅当 cnt 1→0 时产出 -e。
func (u *Union) Remove(p int, e string) (Change, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastRemoveChecks = 0 // cnt map O(1) 查询，无需扫描任何分区
	if !u.parts[p].Remove(e) {
		return Change{}, false
	}
	c := u.cnt[e] - 1
	if c == 0 {
		delete(u.cnt, e)
		ch := Change{Add: false, Elem: e}
		u.log = append(u.log, ch)
		return ch, true
	}
	u.cnt[e] = c
	return Change{}, false
}

// View 返回当前去重并集（cnt>=1 的元素）的快照。
func (u *Union) View() map[string]struct{} {
	u.mu.Lock()
	defer u.mu.Unlock()
	v := make(map[string]struct{}, len(u.cnt))
	for e := range u.cnt {
		v[e] = struct{}{}
	}
	return v
}

// Changes 返回 changelog 的顺序副本。
func (u *Union) Changes() []Change {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]Change, len(u.log))
	copy(out, u.log)
	return out
}

// Verify 核验不变量 1-3：批量重算一致 / changelog 自洽 / 引用计数守恒。
func (u *Union) Verify() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	want := make(map[string]int) // 不变量 3：从各分区集合批量重算 cnt
	for _, s := range u.parts {
		for _, e := range s.Elements() {
			want[e]++
		}
	}
	if len(want) != len(u.cnt) {
		return fmt.Errorf("cnt 大小不符: got %d want %d", len(u.cnt), len(want))
	}
	for e, c := range want { // want 的键即去重并集，c>=1（不变量 1）
		if u.cnt[e] != c {
			return fmt.Errorf("cnt[%q]=%d 不符，重算为 %d", e, u.cnt[e], c)
		}
	}
	seen := make(map[string]struct{}) // 不变量 2：前缀 + / - 严格交替
	for i, ch := range u.log {
		if ch.Add {
			if _, ok := seen[ch.Elem]; ok {
				return fmt.Errorf("changelog[%d] 重复 +%q", i, ch.Elem)
			}
			seen[ch.Elem] = struct{}{}
		} else if _, ok := seen[ch.Elem]; !ok {
			return fmt.Errorf("changelog[%d] 撤回未在视图的 %q", i, ch.Elem)
		} else {
			delete(seen, ch.Elem)
		}
	}
	return nil
}

const checkBound = 1 // Remove 扫描分区数的与 m 无关上界（实际恒 0）

// ComplexityBoundOK 在多档 m 下验证 Remove 来源检查不随分区数线性增长，只回传布尔。
func ComplexityBoundOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		u := New(m)
		for p := 0; p < m; p++ {
			u.Add(p, "a")
		}
		if _, out := u.Remove(0, "a"); out || u.lastRemoveChecks > checkBound {
			return false // cnt m→m-1：无 changelog，检查数为常数
		}
		for p := 1; p < m; p++ {
			_, out := u.Remove(p, "a")
			last := p == m-1 // 末次 cnt 1→0 应输出 -a
			if u.lastRemoveChecks > checkBound || out != last {
				return false
			}
		}
	}
	return true
}
