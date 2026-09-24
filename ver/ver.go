// Package ver 定义不可变版本：一个 id 加一张只读 map。
// 版本一旦创建，其 map 永不写入；所有"修改"都通过 Clone 产生新 map。
package ver

// Version 是一个不可变快照。Id 单调递增，M 的内容绝不原地修改。
type Version struct {
	Id int
	M  map[string]string
}

// New 构造空表初始版本（id=0）。
func New() *Version {
	return &Version{Id: 0, M: map[string]string{}}
}

// Clone 返回当前版本 map 的深拷贝，供写者在其上修改后发布为新版本。
// 原版本的 map 不受影响。
func (v *Version) Clone() map[string]string {
	m := make(map[string]string, len(v.M)+1)
	for k, val := range v.M {
		m[k] = val
	}
	return m
}

// Get 查询单个 key，返回值与是否存在。
func (v *Version) Get(k string) (string, bool) {
	val, ok := v.M[k]
	return val, ok
}

// Len 返回版本内 key 的总数。
func (v *Version) Len() int {
	return len(v.M)
}
