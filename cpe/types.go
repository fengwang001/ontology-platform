package cpe

import "fmt"

// Category 学分类别。
type Category int

const (
	Mandatory Category = iota // 必修类
	Elective                  // 选修类
	Online                    // 线上类
)

func (c Category) String() string {
	switch c {
	case Mandatory:
		return "mandatory"
	case Elective:
		return "elective"
	case Online:
		return "online"
	default:
		return "unknown"
	}
}

func (c Category) valid() bool { return c == Mandatory || c == Elective || c == Online }

// CertStatus 证书状态。
type CertStatus int

const (
	CertActive CertStatus = iota
	CertExpired
)

func (s CertStatus) String() string {
	if s == CertExpired {
		return "expired"
	}
	return "active"
}

// CyclePhase 周期阶段。
type CyclePhase int

const (
	PhaseOpen   CyclePhase = iota // 周期内
	PhaseGrace                    // 宽限期
	PhaseClosed                   // 已关闭（达标或失败）
)

func (p CyclePhase) String() string {
	switch p {
	case PhaseOpen:
		return "open"
	case PhaseGrace:
		return "grace"
	default:
		return "closed"
	}
}

// Tally 周期内各类原始累计（选修含结转计入部分）。
type Tally struct {
	Mandatory int
	Elective  int
	Online    int
}

func (t *Tally) add(cat Category, n int) {
	switch cat {
	case Mandatory:
		t.Mandatory += n
	case Elective:
		t.Elective += n
	case Online:
		t.Online += n
	}
}

// Counted 封顶与联合约束后的计入量。
type Counted struct {
	Mandatory int
	Elective  int
	Online    int
	Total     int
}

// CycleView 单个周期（含当前周期）的核算视图。
type CycleView struct {
	Index          int
	Start          int // 左闭
	End            int // 右开
	Phase          CyclePhase
	Passed         bool // 已关闭时有效
	PassedInGrace  bool // 已关闭且达标时，是否在宽限期内补足
	GraceEntered   bool
	CarryIn        int // 上一周期结转入（按选修计入）
	Raw            Tally
	Counted        Counted
	MeetsTotal     bool
	MeetsMandatory bool
	Pass           bool // 未关闭时为按当前累计的试算结果
	CarryOut       int  // 已关闭时为最终结转；未关闭时为当前试算结转
	GraceStart     int  // 宽限期首日（= End）
	GraceEnd       int  // 宽限期结束时刻（= End + GraceDays）
}

func (v CycleView) String() string {
	return fmt.Sprintf("cycle#%d[%d,%d) %s pass=%v M=%d E=%d O=%d T=%d carryIn=%d carryOut=%d",
		v.Index, v.Start, v.End, v.Phase, v.Pass,
		v.Counted.Mandatory, v.Counted.Elective, v.Counted.Online, v.Counted.Total,
		v.CarryIn, v.CarryOut)
}

// View 持证人某一时刻的完整核算视图。
type View struct {
	HolderID   string
	Generation int // 重新注册（换发）的代数，从 0 开始
	AsOf       int
	Cert       CertStatus
	Cycles     []CycleView
}

func (v View) String() string {
	s := fmt.Sprintf("holder=%s gen=%d asOf=%d cert=%s", v.HolderID, v.Generation, v.AsOf, v.Cert)
	for _, c := range v.Cycles {
		s += "\n  " + c.String()
	}
	return s
}
