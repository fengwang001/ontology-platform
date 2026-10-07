package ontology

// Input 是动作调用的输入。
type Input map[string]any

// Output 是动作执行逻辑的输出。
type Output map[string]any

// Logic 是某个对象类型为某个动作注册的具体执行逻辑。
// Pre/Post 为该逻辑自身的前置/后置条件；Execute 为实际执行。
type Logic struct {
	ID      string
	Pre     func(obj *Object, in Input) error
	Execute func(obj *Object, in Input) (Output, error)
	Post    func(obj *Object, in Input, out Output) error
}

// Action 是动作的通用声明，只规定输入输出形状与契约，不含具体执行逻辑。
type Action struct {
	Name string
	// Validate 校验通用声明规定的输入形状（可选）。
	Validate func(in Input) error
}

// installState 区分“从未注册”“当前直接注册生效”“显式放弃直接处理”。
type installState int

const (
	stateAbsent installState = iota
	stateActive
	stateWaived
)

// installNode 是某 (动作, 具体类型) 槽位上的一次注册。
// prev 保留上一次注册，用于热替换后的在途调用与事后审计。
type installNode struct {
	logic *Logic
	state installState
	relax RelaxScope
	seq   int64
	prev  *installNode
}

// DispatchStatus 描述一次调用的最终归类。
type DispatchStatus string

const (
	StatusOK                  DispatchStatus = "ok"
	StatusNoDispatchNone      DispatchStatus = "no-dispatch:none-registered"
	StatusNoDispatchWaived    DispatchStatus = "no-dispatch:waived-without-inherited"
	StatusRevokedDuringLookup DispatchStatus = "object-revoked-during-lookup"
	StatusPreconditionFailed  DispatchStatus = "precondition-failed"
	StatusPostconditionFailed DispatchStatus = "postcondition-failed"
	StatusExecuteFailed       DispatchStatus = "execute-failed"
)

// HitBasis 记录命中依据。
type HitBasis string

const (
	HitDirect    HitBasis = "direct"
	HitInherited HitBasis = "inherited"
	HitNone      HitBasis = "none"
)

// DispatchRecord 记录每次调用的分派路径、命中依据与最终结果，供事后核对。
type DispatchRecord struct {
	Seq          int64
	Action       string
	ObjectID     string
	ConcreteType string
	Path         []string // 查找时遍历的类型链（自具体类型向上）
	Steps        int      // 链上实际检查过的层数
	HitBasis     HitBasis
	HitType      string
	LogicID      string
	RegistryVer  int64
	Status       DispatchStatus
	Output       Output
	Err          string
}
