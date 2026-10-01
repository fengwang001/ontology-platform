package fdb

// naiveEntry 是参考实现使用的无映射表项。
type naiveEntry struct {
	v      int
	mac    MAC
	port   int
	seen   int64
	static bool
}

// Naive 是严格按规格逐条扫描写成的参考实现：
// 不用哈希表、不做任何索引，所有操作都线性扫描整个切片。
// 它与 FDB 的对外行为与计数口径完全一致，供差分对照。
type Naive struct {
	n, c  int
	a     int64
	table []naiveEntry
	lastT int64
	stats Stats
}

// NewNaive 以与 New 相同的参数构造参考实现。
func NewNaive(n int, a int64, c int) (*Naive, error) {
	if n < 1 || n > 64 || a <= 0 || c <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Naive{n: n, c: c, a: a}, nil
}

func (m *Naive) find(v int, mac MAC) int {
	for i := range m.table {
		if m.table[i].v == v && m.table[i].mac == mac {
			return i
		}
	}
	return -1
}

// sweep 逐条删除全部已过期动态表项；为保持结果与 map 实现一致，
// 计数只与被删条数有关，与删除顺序无关。
func (m *Naive) sweep(t int64) {
	kept := m.table[:0]
	for _, e := range m.table {
		if !e.static && t-e.seen >= m.a {
			m.stats.Expired++
			continue
		}
		kept = append(kept, e)
	}
	m.table = kept
}

func (m *Naive) dynLen() int {
	n := 0
	for _, e := range m.table {
		if !e.static {
			n++
		}
	}
	return n
}

// evict 全表扫描选 seen 最小者，并列取 (v,mac) 字节序最小者。
func (m *Naive) evict() {
	victim := -1
	for i := range m.table {
		if m.table[i].static {
			continue
		}
		if victim == -1 {
			victim = i
			continue
		}
		cur, best := m.table[i], m.table[victim]
		if cur.seen < best.seen ||
			(cur.seen == best.seen && keyLess(key(cur.v, cur.mac), key(best.v, best.mac))) {
			victim = i
		}
	}
	m.table = append(m.table[:victim], m.table[victim+1:]...)
	m.stats.Evictions++
}

func (m *Naive) beginChange(p, v int, t int64) error {
	if p < 0 || p >= m.n {
		return ErrPortRange
	}
	if !validVLAN(v) {
		return ErrVLANRange
	}
	if t < m.lastT {
		return ErrClockBackward
	}
	m.sweep(t)
	m.lastT = t
	return nil
}

// Frame 逐条扫描版的入帧处理，顺序与 FDB.Frame 完全相同。
func (m *Naive) Frame(p int, s, d MAC, v int, t int64) ([]int, error) {
	if err := m.beginChange(p, v, t); err != nil {
		return nil, err
	}

	if !IsMulticast(s) {
		i := m.find(v, s)
		if i >= 0 {
			e := &m.table[i]
			if e.static {
				if e.port != p {
					m.stats.SecurityDrops++
					return []int{}, nil
				}
			} else {
				if e.port != p {
					m.stats.Moves++
				}
				e.port = p
				e.seen = t
			}
		} else {
			if m.dynLen() >= m.c {
				m.evict()
			}
			m.table = append(m.table, naiveEntry{v: v, mac: s, port: p, seen: t})
			m.stats.Learned++
		}
	}

	if IsMulticast(d) {
		m.stats.Floods++
		return m.flood(p), nil
	}
	if i := m.find(v, d); i >= 0 {
		if m.table[i].port == p {
			m.stats.Filtered++
			return []int{}, nil
		}
		return []int{m.table[i].port}, nil
	}
	m.stats.Floods++
	return m.flood(p), nil
}

func (m *Naive) flood(p int) []int {
	out := make([]int, 0, m.n-1)
	for q := 0; q < m.n; q++ {
		if q != p {
			out = append(out, q)
		}
	}
	return out
}

// AddStatic 逐条扫描版静态表项安装。
func (m *Naive) AddStatic(v int, mac MAC, p int, t int64) error {
	if IsMulticast(mac) {
		return ErrMulticastStatic
	}
	if err := m.beginChange(p, v, t); err != nil {
		return err
	}
	if i := m.find(v, mac); i >= 0 {
		if !m.table[i].static {
			m.table = append(m.table[:i], m.table[i+1:]...)
			m.stats.Overridden++
		} else {
			m.table[i].port = p
			m.table[i].seen = t
			return nil
		}
	}
	m.table = append(m.table, naiveEntry{v: v, mac: mac, port: p, seen: t, static: true})
	return nil
}

// FlushPort 逐条扫描版端口动态表项清除。
func (m *Naive) FlushPort(p int, t int64) (int, error) {
	if err := m.beginChange(p, 1, t); err != nil {
		return 0, err
	}
	removed := 0
	kept := m.table[:0]
	for _, e := range m.table {
		if !e.static && e.port == p {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	m.table = kept
	m.stats.Flushed += int64(removed)
	return removed, nil
}

// Lookup 只读查询，过期动态表项视为未命中。
func (m *Naive) Lookup(v int, mac MAC, t int64) (int, bool, error) {
	if !validVLAN(v) {
		return 0, false, ErrVLANRange
	}
	i := m.find(v, mac)
	if i < 0 {
		return 0, false, nil
	}
	e := m.table[i]
	if !e.static && t-e.seen >= m.a {
		return 0, false, nil
	}
	return e.port, true, nil
}

// Len 返回动态表项数（含已过期但尚未清除者）。
func (m *Naive) Len() int { return m.dynLen() }

// Stats 返回计数快照。
func (m *Naive) Stats() Stats { return m.stats }
