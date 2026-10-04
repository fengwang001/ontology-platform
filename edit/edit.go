package edit

import "errors"

const (
	maxTime      = 1_000_000_000
	maxEntries   = 10_000
	maxInsertLen = 1_000_000_000
)

type Delete struct {
	A int64
	B int64
}

type Insert struct {
	At  int64
	Len int64
}

type Table struct {
	Deletes []Delete
	Inserts []Insert
}

var ErrInvalidTable = errors.New("edit: invalid edit table")

type Compiled struct {
	// 删除段按 A 升序（已保证互不重叠、相接允许）。
	delA   []int64
	delB   []int64
	delPre []int64 // delPre[i] = 前 i 段长度之和
	// 插入点按 At 升序、互不相同。
	insAt  []int64
	insPre []int64 // insPre[i] = 前 i 个插入长度之和
	// probes 为本编译表自上次重置以来，映射查询与边界枚举触碰剪辑表项的
	// 比较总次数（非导出，仅供复杂度证明与测试使用）。
	probes int64
}

// Compile 校验并编译剪辑表。输入不满足规格时返回 ErrInvalidTable。
func Compile(t Table) (*Compiled, error) {
	if len(t.Deletes) > maxEntries || len(t.Inserts) > maxEntries {
		return nil, ErrInvalidTable
	}
	c := &Compiled{}
	// 删除段：0 <= a < b <= 1e9，按 a 升序、互不重叠（相接允许）。
	for i, d := range t.Deletes {
		if d.A < 0 || d.B < 0 || d.A > maxTime || d.B > maxTime || d.A >= d.B {
			return nil, ErrInvalidTable
		}
		if i > 0 && d.A < t.Deletes[i-1].B {
			return nil, ErrInvalidTable
		}
		c.delA = append(c.delA, d.A)
		c.delB = append(c.delB, d.B)
	}
	// 插入点：0 <= at <= 1e9，1 <= len，at 升序且互不相同。
	for i, in := range t.Inserts {
		if in.At < 0 || in.At > maxTime || in.Len < 1 || in.Len > maxInsertLen {
			return nil, ErrInvalidTable
		}
		if i > 0 && in.At <= t.Inserts[i-1].At {
			return nil, ErrInvalidTable
		}
		c.insAt = append(c.insAt, in.At)
	}
	// 插入点不得严格落在删除段内部（a < at < b 非法）。
	for _, in := range t.Inserts {
		// 二分找到最后一个 A <= at 的删除段。
		lo, hi := 0, len(c.delA)
		for lo < hi {
			m := int(uint(lo+hi) >> 1)
			if c.delA[m] <= in.At {
				lo = m + 1
			} else {
				hi = m
			}
		}
		if j := lo - 1; j >= 0 && c.delA[j] < in.At && in.At < c.delB[j] {
			return nil, ErrInvalidTable
		}
	}
	// 前缀和。
	c.delPre = make([]int64, len(c.delA)+1)
	for i, b := range c.delB {
		c.delPre[i+1] = c.delPre[i] + (b - c.delA[i])
	}
	c.insPre = make([]int64, len(c.insAt)+1)
	for i, in := range t.Inserts {
		c.insPre[i+1] = c.insPre[i] + in.Len
	}
	return c, nil
}

// lowerBound 返回最小的满足 a[i] >= x 的下标（比较次数计入 probes）。
func (c *Compiled) lowerBound(a []int64, x int64) int {
	lo, hi := 0, len(a)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		c.probes++
		if a[m] < x {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// upperBound 返回最小的满足 a[i] > x 的下标。
func (c *Compiled) upperBound(a []int64, x int64) int {
	lo, hi := 0, len(a)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		c.probes++
		if a[m] <= x {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// Del 返回各删除段与 [0,t) 交集的总长。
func (c *Compiled) Del(t int64) int64 {
	if t <= 0 {
		return 0
	}
	i := c.lowerBound(c.delA, t) // 段 [0,i) 的 A < t
	if i == 0 {
		return 0
	}
	d := c.delPre[i]
	if b := c.delB[i-1]; b > t {
		d -= b - t // 最后一段只交接到 t
	}
	return d
}

// InsL 返回 at 严格小于 t 的插入长度之和。
func (c *Compiled) InsL(t int64) int64 {
	return c.insPre[c.lowerBound(c.insAt, t)]
}

// InsR 返回 at 不大于 t 的插入长度之和。
func (c *Compiled) InsR(t int64) int64 {
	return c.insPre[c.upperBound(c.insAt, t)]
}

// FL 为左映射 fL(t) = t - del(t) + insL(t)。
func (c *Compiled) FL(t int64) int64 { return t - c.Del(t) + c.InsL(t) }

// FR 为右映射 fR(t) = t - del(t) + insR(t)。
func (c *Compiled) FR(t int64) int64 { return t - c.Del(t) + c.InsR(t) }

// Probes 返回累计比较计数并清零。
func (c *Compiled) Probes() int64 {
	p := c.probes
	c.probes = 0
	return p
}

// probeLimit 返回每片映射比较数预算 4*ceil(log2(k+2))。
func probeLimit(k int) int {
	return 4 * ceilLog2(k+2)
}

// ceilLog2 返回 ceil(log2(n))（n >= 1）。
func ceilLog2(n int) int {
	u := uint(n)
	ceil := 0
	for (uint(1) << uint(ceil)) < u {
		ceil++
	}
	return ceil
}
