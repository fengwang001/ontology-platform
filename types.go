package audit

// 本文件定义培养方案、要求树、修读记录、替换与转入学分等核心数据结构。

// Course 课程主数据。Credits 为方案认定的标准学分。
type Course struct {
	ID      string
	Credits float64
}

// ReqKind 要求类型。
type ReqKind int

const (
	ReqLeaf     ReqKind = iota // 叶子：学分 + 门数
	ReqInternal                // 内部：子要求中至少满足 MinChildren 个
)

// Requirement 培养方案要求树上的一个节点。
// 必填 ID 在同一方案版本内唯一且可按字典序比较。
type Requirement struct {
	ID          string
	Kind        ReqKind
	Required    bool     // 是否必修类叶子（仅叶子使用）
	Courses     []string // 叶子：可计入课程 ID 列表
	MinCredits  float64  // 叶子：最低学分（含等号）
	MinCourses  int      // 叶子：最低门数（含等号）
	MinChildren int      // 内部：至少满足的子要求个数
	Children    []*Requirement
}

// PlanVersion 培养方案的一个版本。Version 数值越大版本越新。
type PlanVersion struct {
	PlanID         string
	Version        int
	Root           *Requirement
	MinTotalCredit float64 // 毕业最低总学分
	PassLine       float64 // 及格线（成绩 >= 及格线才可计入，含等号）
	MinGPA         float64 // 加权平均成绩下限（含等号）
	TransferCap    float64 // 转入学分上限
	// SharedCredit 声明显式允许重复计入的叶子要求对（双向匹配）。
	SharedCredit map[[2]string]bool
}

// Record 一条本校修读记录。
type Record struct {
	ID       string
	Student  string
	Course   string
	Semester string // 学期标识，按登记的学期序比较（如 "2023-1"）
	Credits  float64
	Grade    float64
	Revoked  bool
}

// Substitution 审批通过的课程替换：From 可视同 To 计入。
type Substitution struct {
	ID          string
	From        string
	To          string
	PlanID      string
	PlanVersion int    // 仅适用于该方案版本
	Effective   string // 生效学期；修读学期 >= Effective 才可替换（含等号）
}

// TransferRecord 外校转入学分记录，按登记次序获得序号（规则的一部分）。
type TransferRecord struct {
	Seq      int
	Student  string
	Course   string // 以本校课程身份计入
	Credits  float64
	Grade    float64
	Semester string
}

// Student 学生及其绑定的方案版本。
type Student struct {
	ID          string
	PlanID      string
	PlanVersion int
}

// ErrorCode 操作错误分类，优先级见 ErrPriority。
type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota + 1
	ErrNotFound
	ErrRecordRevoked
	ErrSubNotApplicable
	ErrTransferCap
	ErrVersionTooOld
)

// OpError 带固定错误分类的操作错误。
type OpError struct {
	Code    ErrorCode
	Message string
}

func (e *OpError) Error() string { return e.Message }

// ErrPriority 返回错误分类的固定优先级（数值越小优先级越高）。
func ErrPriority(c ErrorCode) int {
	switch c {
	case ErrInvalidArgument:
		return 1
	case ErrNotFound:
		return 2
	case ErrRecordRevoked:
		return 3
	case ErrSubNotApplicable:
		return 4
	case ErrTransferCap:
		return 5
	case ErrVersionTooOld:
		return 6
	default:
		return 99
	}
}

func validatePlan(p *PlanVersion) error {
	if p == nil || p.PlanID == "" || p.Version <= 0 || p.Root == nil {
		return &OpError{Code: ErrInvalidArgument, Message: "invalid plan"}
	}
	if p.MinTotalCredit < 0 || p.PassLine < 0 || p.MinGPA < 0 || p.TransferCap < 0 {
		return &OpError{Code: ErrInvalidArgument, Message: "invalid plan thresholds"}
	}
	seen := map[string]bool{}
	var walk func(r *Requirement) error
	walk = func(r *Requirement) error {
		if r == nil || r.ID == "" || seen[r.ID] {
			return &OpError{Code: ErrInvalidArgument, Message: "invalid requirement id"}
		}
		seen[r.ID] = true
		switch r.Kind {
		case ReqLeaf:
			if r.MinCredits < 0 || r.MinCourses < 0 {
				return &OpError{Code: ErrInvalidArgument, Message: "invalid leaf thresholds"}
			}
		case ReqInternal:
			if len(r.Children) == 0 || r.MinChildren <= 0 || r.MinChildren > len(r.Children) {
				return &OpError{Code: ErrInvalidArgument, Message: "invalid internal requirement"}
			}
			for _, c := range r.Children {
				if err := walk(c); err != nil {
					return err
				}
			}
		default:
			return &OpError{Code: ErrInvalidArgument, Message: "unknown requirement kind"}
		}
		return nil
	}
	if err := walk(p.Root); err != nil {
		return err
	}
	for pair := range p.SharedCredit {
		if !seen[pair[0]] || !seen[pair[1]] {
			return &OpError{Code: ErrInvalidArgument, Message: "shared-credit pair references unknown requirement"}
		}
	}
	return nil
}

func cloneReq(r *Requirement) *Requirement {
	cp := &Requirement{
		ID: r.ID, Kind: r.Kind, Required: r.Required,
		Courses:    append([]string(nil), r.Courses...),
		MinCredits: r.MinCredits, MinCourses: r.MinCourses,
		MinChildren: r.MinChildren,
	}
	for _, c := range r.Children {
		cp.Children = append(cp.Children, cloneReq(c))
	}
	return cp
}

func clonePlan(p *PlanVersion) *PlanVersion {
	cp := &PlanVersion{
		PlanID: p.PlanID, Version: p.Version, Root: cloneReq(p.Root),
		MinTotalCredit: p.MinTotalCredit, PassLine: p.PassLine,
		MinGPA: p.MinGPA, TransferCap: p.TransferCap,
	}
	if len(p.SharedCredit) > 0 {
		cp.SharedCredit = map[[2]string]bool{}
		for k, v := range p.SharedCredit {
			cp.SharedCredit[k] = v
		}
	}
	return cp
}

func planContainsCourse(p *PlanVersion, course string) bool {
	var walk func(r *Requirement) bool
	walk = func(r *Requirement) bool {
		if r.Kind == ReqLeaf {
			for _, c := range r.Courses {
				if c == course {
					return true
				}
			}
			return false
		}
		for _, c := range r.Children {
			if walk(c) {
				return true
			}
		}
		return false
	}
	return walk(p.Root)
}
