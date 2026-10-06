package speq

// InspectionResult 检验结果。
type InspectionResult int

const (
	// ResultPass 合格。
	ResultPass InspectionResult = iota
	// ResultConditional 有条件合格，须附带整改限期天数。
	ResultConditional
	// ResultFail 不合格：立即停用，到期日不变。
	ResultFail
)

// obj 系统内部对象（设备或附件）。
type obj struct {
	id       string
	category string
	kind     ObjectKind

	scrapped bool
	sealed   bool
	disabled bool

	// expiry 到期日（有效期含当天；expiry 次日起超期）。
	expiry int

	// sealDate 封存操作日期。
	sealDate int
	// sealAnchor 封存期内最近一次检验日期；用于启封时只顺延检验之后经过的封存天数。
	sealAnchor int

	// host 附件所在设备编号；设备自身该字段为空。
	host string
}

// expired 以查询日 onDate 判断是否超期（封存期间暂停计时，永不超期）。
func (o *obj) expired(onDate int) bool {
	if o.sealed {
		return false
	}
	return onDate > o.expiry
}

// Snapshot 对象状态快照（供测试与文档展示）。
type Snapshot struct {
	ID       string
	Category string
	Kind     ObjectKind
	Scrapped bool
	Sealed   bool
	Disabled bool
	Expiry   int
	Host     string
}

// WarningEntry 预警条目。
//
// 设备因附件临近到期时，Triggers 给出触发的附件编号（编号升序）；
// 其余情况 Triggers 为空。
type WarningEntry struct {
	ID         string
	Kind       ObjectKind
	Expiry     int
	SortExpiry int
	Triggers   []string
}
