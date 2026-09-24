// Package dict 维护 value→code 的定容量字典。
//
// code 按 value 首次出现的顺序从 0 开始分配；字典是否已满、何时边界重置
// 由上层（enc 包）决定，本包只负责映射、满判定与清空，不依赖其他包。
package dict

// Dict 是 value→code 的定容量映射。零值不可用，必须用 New 创建。
type Dict struct {
	m      map[string]int
	limit  int
	probes int // 最近一次命中判定探测过的字典条目数；非导出，仅供同包白盒测试读取
}

// New 创建容量为 k（>0）的空字典。
func New(k int) *Dict {
	return &Dict{m: make(map[string]int, k), limit: k}
}

// Full 报告字典是否已容纳 limit 个不同 value。
func (d *Dict) Full() bool { return len(d.m) >= d.limit }

// Lookup 判定 value 是否命中字典并返回其 code。
//
// 哈希映射按 key 一次定位，无论表内已有多少条目，探测的条目数恒为 1
// （空表也需一次哈希探测），即 O(1) 而非线性扫描。
func (d *Dict) Lookup(value string) (code int, ok bool) {
	d.probes = 1
	code, ok = d.m[value]
	return code, ok
}

// Add 登记一个新 value（调用方须保证 !Full 且 value 不在表中），
// 返回其 code，code = 登记前的条目数，故首次出现顺序即 code 递增顺序。
func (d *Dict) Add(value string) int {
	code := len(d.m)
	d.m[value] = code
	return code
}

// Reset 清空字典：边界重置后下一个新值重新从 code 0 起。
func (d *Dict) Reset() { d.m = make(map[string]int, d.limit) }

// Len 返回当前字典条目数。
func (d *Dict) Len() int { return len(d.m) }
