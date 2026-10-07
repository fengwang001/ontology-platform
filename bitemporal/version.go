package bitemporal

import "sort"

// FormatVersion 描述一个快照导出格式版本对双时态信息的记录约定。
type FormatVersion struct {
	Name    string
	Records map[Axis]AxisSpec
}

// AxisSpec 描述某条时间轴在该格式版本中的记录约定。
type AxisSpec struct {
	Recorded   bool
	StartBound BoundMode
	EndBound   BoundMode
}

// Registry 是格式版本注册表。
type Registry struct {
	versions map[string]FormatVersion
}

// 内置格式版本：覆盖“两条时间轴各自记录与否”的全部 4 种组合，
// 每种组合各提供闭区间与半开区间两种边界约定，共 8 个版本。
//
//	v1/v2：两条时间轴都记录（双时态），闭/半开
//	v3/v4：仅事务时间轴，闭/半开
//	v5/v6：仅有效时间轴，闭/半开
//	v7/v8：两条时间轴都不记录（纯属性快照）
func builtinVersions() []FormatVersion {
	both := func(b BoundMode) map[Axis]AxisSpec {
		return map[Axis]AxisSpec{
			ValidTime:       {Recorded: true, StartBound: Closed, EndBound: b},
			TransactionTime: {Recorded: true, StartBound: Closed, EndBound: b},
		}
	}
	only := func(a Axis, b BoundMode) map[Axis]AxisSpec {
		return map[Axis]AxisSpec{a: {Recorded: true, StartBound: Closed, EndBound: b}}
	}
	return []FormatVersion{
		{Name: "v1", Records: both(Closed)},
		{Name: "v2", Records: both(HalfOpen)},
		{Name: "v3", Records: only(TransactionTime, Closed)},
		{Name: "v4", Records: only(TransactionTime, HalfOpen)},
		{Name: "v5", Records: only(ValidTime, Closed)},
		{Name: "v6", Records: only(ValidTime, HalfOpen)},
		{Name: "v7", Records: map[Axis]AxisSpec{}},
		{Name: "v8", Records: map[Axis]AxisSpec{}},
	}
}

// DefaultRegistry 返回内置版本注册表。
func DefaultRegistry() *Registry {
	reg := &Registry{versions: map[string]FormatVersion{}}
	for _, v := range builtinVersions() {
		reg.versions[v.Name] = v
	}
	return reg
}

// Get 查询已注册版本。
func (reg *Registry) Get(name string) (FormatVersion, bool) {
	v, ok := reg.versions[name]
	return v, ok
}

// Register 注册（或覆盖）一个格式版本。
func (reg *Registry) Register(v FormatVersion) {
	if reg.versions == nil {
		reg.versions = map[string]FormatVersion{}
	}
	reg.versions[v.Name] = v
}

// Names 返回注册表中全部版本名（字典序，保证调用结果稳定）。
func (reg *Registry) Names() []string {
	names := make([]string, 0, len(reg.versions))
	for n := range reg.versions {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// AxisSpecOf 返回某版本对指定时间轴的记录约定；该轴未记录时 Recorded=false。
func (v FormatVersion) AxisSpecOf(a Axis) AxisSpec {
	return v.Records[a]
}
