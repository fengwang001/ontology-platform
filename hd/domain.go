package hd

// Zone 机位区域。
type Zone int

const (
	ZoneGeneral   Zone = iota // 普通区
	ZoneIsolation             // 隔离区
)

func (z Zone) String() string {
	if z == ZoneIsolation {
		return "ISOLATION"
	}
	return "GENERAL"
}

// Infection 患者感染状态。
type Infection int

const (
	InfectionNegative Infection = iota // 阴性
	InfectionHBV                       // 乙肝阳性
	InfectionHCV                       // 丙肝阳性
	InfectionPending                   // 状态待定
)

func (i Infection) String() string {
	switch i {
	case InfectionHBV:
		return "HBV"
	case InfectionHCV:
		return "HCV"
	case InfectionPending:
		return "PENDING"
	default:
		return "NEGATIVE"
	}
}

func (i Infection) valid() bool {
	return i >= InfectionNegative && i <= InfectionPending
}

// Bay 机位。普通区机位可标记为观察位供待定患者使用。
type Bay struct {
	ID        string
	Zone      Zone
	Observed  bool
	Available bool
}

// Patient 患者。
type Patient struct {
	ID        string
	Infection Infection
}

// Config 全局消毒与恢复配置，时长单位均为分钟。
type Config struct {
	CleanNegative int // 阴性（含待定按阴性常规消毒）
	CleanHBV      int // 乙肝阳性后常规消毒
	CleanHCV      int // 丙肝阳性后常规消毒
	DeepClean     int // 隔离区相邻治疗感染类型不同时的深度消毒
	MinRecovery   int // 同一患者相邻两次治疗间的最短恢复间隔
}

// cleanDuration 返回某感染状态治疗结束后的消毒占用时长。
// 待定患者仅能在观察位治疗，其消毒按普通常规消毒处理。
func (c Config) cleanDuration(inf Infection) int {
	switch inf {
	case InfectionHBV:
		return c.CleanHBV
	case InfectionHCV:
		return c.CleanHCV
	default:
		return c.CleanNegative
	}
}

// Treatment 一次治疗占用记录。
// 占用区间为 [Start, End)，之后附带 [End, End+clean) 的消毒占用。
type Treatment struct {
	ID        string
	PatientID string
	BayID     string
	Start     int
	End       int
	Duration  int
	Infection Infection // 接受治疗时按当时状态记录；改派/状态变更时同步更新
	PlanID    string    // 空串表示单次治疗
	Canceled  bool
}

// Plan 周期性治疗方案。
type Plan struct {
	ID        string
	PatientID string
	Weekdays  map[int]bool // 0=周一 ... 6=周日（周起点为 7 的整数倍日期）
	DayStart  int          // 日内开始时刻 [0,1440)
	Duration  int
	From      int // 生效起始日期（绝对分钟，须为 1440 的整数倍）
	To        int // 生效结束日期（含）
	Canceled  bool
}

// Fault 机位故障停用记录。
type Fault struct {
	BayID     string
	Start     int // 故障时刻（含）
	Recovered bool
	RecoverAt int // 恢复可用时刻（含）；未恢复为 -1
}

const minutesPerDay = 1440
const minutesPerWeek = 7 * minutesPerDay
const maxTime = 10_000_000
