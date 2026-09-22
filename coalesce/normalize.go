package coalesce

import "ontology/rangespec"

// Normalize 把解析后的区间规格结合资源长度归一化为闭区间列表：
// 先按各写法的语义裁剪越界部分（越界不报错），再按起点升序排序，
// 最后把重叠与首尾相邻的区间合并。返回的区间互不重叠、互不相邻，
// 且覆盖的字节集合与裁剪后的原始集合完全相同。
//
// 不可满足（起点越过末尾、bytes=-0、空集合、size<0）返回
// *UnsatisfiableError。
func Normalize(specs []rangespec.Spec, size int64) ([]Interval, error) {
	n := NewNormalizer()
	return n.Normalize(specs, size)
}
