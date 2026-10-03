package window

import "strings"

// Counter 是近似滑动窗口计数器，只保留三个整数。
// k 为 cur 所属窗口索引；prev 为紧邻上一窗口的计数。
type Counter struct {
	k    int64
	cur  int64
	prev int64
}

// Key 是计数器的归属键：规则 id 加描述符中该模式各键的实际取值。
type Key struct {
	RuleID string
	Values []string
}

// Table 持有全部计数器。零值即可用。
type Table struct {
	m map[string]*Counter
}

// NewTable 创建空计数器表。
func NewTable() *Table {
	return &Table{m: make(map[string]*Counter)}
}

// Sig 返回键的稳定字符串表示（id 与各取值用 NUL 分隔）。
func (k Key) Sig() string {
	return k.RuleID + "\x00" + strings.Join(k.Values, "\x00")
}

// Len 返回计数器总数。
func (t *Table) Len() int {
	return len(t.m)
}

// DeleteRule 删除某规则的全部计数器，返回删除数量。
func (t *Table) DeleteRule(ruleID string) int {
	prefix := ruleID + "\x00"
	n := 0
	for s := range t.m {
		if strings.HasPrefix(s, prefix) {
			delete(t.m, s)
			n++
		}
	}
	return n
}

// Estimate 返回键 key 在时刻 now（毫秒）对窗口宽度 w 的估计值。
// 纯只读：不创建计数器，也不改变已有计数器的窗口位置与计数。
func (t *Table) Estimate(key Key, now, w int64) int64 {
	c, ok := t.m[key.Sig()]
	if !ok {
		return 0
	}
	tmp := *c
	tmp.RollTo(now, w)
	return tmp.Est(now, w)
}

// AllowIncr 是两阶段提交的推进步骤：将键滚动到 now 后 cur 加一，
// 封顶 cap；计数器不存在时先在当前窗口创建（cur=prev=0）。
// 返回加一后的估计值（滚动后）。
func (t *Table) AllowIncr(key Key, now, w, cap int64) int64 {
	sig := key.Sig()
	c, ok := t.m[sig]
	if !ok {
		c = &Counter{k: now / w}
		t.m[sig] = c
	}
	c.RollTo(now, w)
	c.Incr(cap)
	return c.Est(now, w)
}

// RollTo 将计数器滚动到 now 所属窗口。
// 新计数器视为已在 k'=now/w 且 cur=prev=0。
func (c *Counter) RollTo(now, w int64) {
	kp := now / w
	switch {
	case kp == c.k:
	case kp == c.k+1:
		c.prev = c.cur
		c.cur = 0
		c.k = kp
	default: // kp >= k+2（时间单调，不处理回退）
		c.prev = 0
		c.cur = 0
		c.k = kp
	}
}

// Est 返回加权估计：cur + floor(prev*(w-now%w)/w)。
func (c *Counter) Est(now, w int64) int64 {
	weight := w - now%w
	return c.cur + (c.prev*weight)/w
}

// Incr 将 cur 加一并封顶 cap（规则的 L+1）。
func (c *Counter) Incr(cap int64) {
	if c.cur < cap {
		c.cur++
	}
}

// Snapshot 是计数器的只读视图，用于测试与精确重放比对。
type Snapshot struct {
	K    int64
	Cur  int64
	Prev int64
}

// Get 返回某键的快照与是否存在，不存在时返回零值快照。
func (t *Table) Get(key Key) (Snapshot, bool) {
	c, ok := t.m[key.Sig()]
	if !ok {
		return Snapshot{}, false
	}
	return Snapshot{K: c.k, Cur: c.cur, Prev: c.prev}, true
}

// Range 遍历全部计数器，fn 收到键签名与快照（只读）。
func (t *Table) Range(fn func(sig string, s Snapshot)) {
	for sig, c := range t.m {
		fn(sig, Snapshot{K: c.k, Cur: c.cur, Prev: c.prev})
	}
}
