package ontology

// 时间以整数分钟计，合法范围 [0, MaxTimeMinutes]。
const MaxTimeMinutes int64 = 10_000_000

const (
	// InfectiousLeadMinutes 传染期自发病时刻前 48 小时（含）起。
	InfectiousLeadMinutes int64 = 48 * 60
	// ContactThresholdMinutes 密接/次密接累计同室时长阈值（恰等于算入）。
	ContactThresholdMinutes int64 = 120
	// CloseIsolationMinutes 密切接触者隔离时长（7 天）。
	CloseIsolationMinutes int64 = 7 * 24 * 60
	// SecondaryObservationMinutes 次密接观察时长（3 天）。
	SecondaryObservationMinutes int64 = 3 * 24 * 60
)

// Level 接触等级，数值越大越严。
type Level int

const (
	LevelSecondary Level = iota + 1 // 次密接
	LevelClose                      // 密切接触者
)

func (l Level) String() string {
	if l == LevelClose {
		return "密切接触者"
	}
	return "次密接"
}

// Status 患者在某一 now 的隔离状态。
type Status int

const (
	StatusNone      Status = iota // 无关
	StatusClose                   // 密切接触者隔离中
	StatusSecondary               // 次密接观察中
	StatusReleased                // 已解除
)

func (s Status) String() string {
	switch s {
	case StatusClose:
		return "密切接触者隔离中"
	case StatusSecondary:
		return "次密接观察中"
	case StatusReleased:
		return "已解除"
	}
	return "无关"
}

// Source 患者状态的一个来源（某个病例下的认定结果），用于判定依据与多病例叠加。
type Source struct {
	CaseID      string
	Level       Level
	LastContact int64 // 最后接触时刻
	ReleaseAt   int64 // 解除时刻（最后接触 + 7 天 / 3 天）
	InPeriod    bool  // 在查询 now 时是否仍在期内
}

// StatusResult 状态查询结果。
type StatusResult struct {
	Status    Status
	ReleaseAt int64    // 解除时刻；StatusNone 时为 0 且无意义
	Sources   []Source // 全部来源（按 CaseID 排序），作为判定依据
}

// Contact 清单查询中的一个接触者条目。
type Contact struct {
	PatientID   string
	Minutes     int64  // 累计重叠时长（次密接为使其达标的那个密接上的累计值）
	LastContact int64  // 最后接触时刻
	Via         string // 仅次密接：使其达标的密切接触者 ID
}

// ContactsResult 某病例当前的接触者清单。
type ContactsResult struct {
	Close     []Contact // 密切接触者（按 PatientID 排序）
	Secondary []Contact // 次密接（按 PatientID 排序）
}

// OpStats 上一次被处理操作的开销统计，用于可验证的性能对照。
type OpStats struct {
	StaysScanned     int      // 检查过的住宿记录条数
	CasesDerived     int      // 实际重新推导的病例数
	CasesInvalidated int      // 被标记失效的病例数
	InvalidatedIDs   []string // 被标记失效的病例 ID（便于断言）
}
