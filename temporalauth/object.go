package temporalauth

// PropertyKind 区分普通属性与时间类属性。
type PropertyKind int

const (
	KindPlain PropertyKind = iota
	KindTemporal
)

// PropertySpec 是某一对象类型版本中的属性规格。
type PropertySpec struct {
	Name       string
	Kind       PropertyKind
	Deprecated bool // 该类型版本下属性是否已被废弃
}

// ObjectTypeVersion 是对象类型的一个不可变版本。属性在某版本中被标记为
// Deprecated 后，查看请求到达该版本生效期即报 ErrPropertyDeprecated。
type ObjectTypeVersion struct {
	ValidFrom  Instant
	Properties map[string]PropertySpec
}

// ObjectType 是对象类型定义的版本链（Version 严格升序）。
type ObjectType struct {
	ID       string
	Versions []ObjectTypeVersion
}

// VersionAt 返回时刻 t 生效的对象类型版本下标（二分，O(log n)）。
func (ot *ObjectType) VersionAt(t Instant) (int, bool) {
	n := len(ot.Versions)
	if n == 0 || t < ot.Versions[0].ValidFrom {
		return 0, false
	}
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if ot.Versions[mid].ValidFrom <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1, true
}

// PropertyAt 返回时刻 t 生效的对象类型版本中指定属性的规格。
func (ot *ObjectType) PropertyAt(prop string, t Instant) (PropertySpec, bool) {
	idx, ok := ot.VersionAt(t)
	if !ok {
		return PropertySpec{}, false
	}
	spec, found := ot.Versions[idx].Properties[prop]
	return spec, found
}

// TemporalRecord 是一条时间类属性取值记录。
type TemporalRecord struct {
	ObjectID   string
	Property   string
	RecordedAt Instant // 录入发生的绝对时刻（UTC）
	RecordZone string  // 录入时标注的录入发生地时区 ID
	Wall       Civil   // 录入时标注的墙钟取值

	// normalizedAt 在录入时一次性冻结：以“录入时刻对象所属地区当时生效的
	// 默认时区版本”为基准归一化得到的绝对时刻。历史记录在任意请求时刻被
	// 反复判定都使用同一基准，不随后续版本迁移而摇摆。
	regionID     string
	normalizedAt Instant
	basisVersion int
}

// NormalizedAt 返回录入时冻结的归一化绝对时刻。
func (r TemporalRecord) NormalizedAt() Instant { return r.normalizedAt }

// BasisVersion 返回录入时冻结的地区默认时区版本下标。
func (r TemporalRecord) BasisVersion() int { return r.basisVersion }

// RegionID 返回对象所属地区 ID。
func (r TemporalRecord) RegionID() string { return r.regionID }
