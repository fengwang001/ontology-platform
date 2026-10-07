package compensation

// Category 是一次动作调用最终判定原因的固定分类，优先级从高到低固定，
// 与补偿是否发生、补偿是否成功无关：
// Contaminated > BusinessRejected > Contention > CompensationFailed。
type Category int

const (
	// CategoryOK 动作全部子操作生效，无补偿。
	CategoryOK Category = iota
	// CategoryCompensationFailed 有子操作失败，补偿过程中至少一个逆操作失败/异常。
	CategoryCompensationFailed
	// CategoryContention 并发争用冲突：守卫已被不相交以外的动作持有。
	CategoryContention
	// CategoryBusinessRejected 子操作本身的业务校验拒绝。
	CategoryBusinessRejected
	// CategoryContaminated 动作涉及的对象实例已处于污染态（最高优先级）。
	CategoryContaminated
)

func (c Category) String() string {
	switch c {
	case CategoryOK:
		return "OK"
	case CategoryCompensationFailed:
		return "COMPENSATION_FAILED"
	case CategoryContention:
		return "CONTENTION"
	case CategoryBusinessRejected:
		return "BUSINESS_REJECTED"
	case CategoryContaminated:
		return "CONTAMINATED"
	default:
		return "UNKNOWN"
	}
}

// OpKind 是副作用子操作的种类。
type OpKind int

const (
	// OpSetProperties 修改一个对象实例的属性集合。
	OpSetProperties OpKind = iota
	// OpCreateLink 创建一条链接实例。
	OpCreateLink
	// OpDeleteLink 删除一条链接实例。
	OpDeleteLink
	// OpHook 在另一个对象实例上级联触发校验钩子。
	OpHook
)

func (k OpKind) String() string {
	switch k {
	case OpSetProperties:
		return "SetProperties"
	case OpCreateLink:
		return "CreateLink"
	case OpDeleteLink:
		return "DeleteLink"
	case OpHook:
		return "Hook"
	default:
		return "Unknown"
	}
}

// SubOp 是动作声明中的一个有序副作用子操作。
type SubOp struct {
	// Kind 子操作种类。
	Kind OpKind
	// ObjectID 被修改属性或触发钩子的对象实例。
	ObjectID string
	// Sets 属性修改集合（仅 OpSetProperties）。
	Sets map[string]any

	// LinkID/From/To/LinkType 描述一条链接实例（链接类子操作使用）。
	LinkID   string
	From     string
	To       string
	LinkType string
	// Hook 级联校验钩子名称（仅 OpHook）。
	Hook string

	// Check 业务校验钩子；返回 false 表示本子操作被业务校验拒绝。
	// 拒绝发生在任何生效之前，nil 视为通过。
	Check func() bool

	// InjectInverseFailure 令本子操作生效后登记的逆操作在补偿时返回失败。
	InjectInverseFailure bool
	// InjectInversePanic 令逆操作在补偿时抛出运行时异常。
	InjectInversePanic bool
}

// Action 是一组按声明顺序依次生效的有序副作用子操作。
type Action struct {
	ID  string
	Ops []SubOp
}

// StepEffect 记录一个子操作的生效结果。
type StepEffect struct {
	Index  int
	Entry  uint64
	Detail string
}

// CompRecord 是补偿流程中单个逆操作环节的结果。
type CompRecord struct {
	Index     int
	Entry     uint64
	ObjectIDs []string
	OK        bool
	Panicked  bool
	Skipped   bool
	Reason    string
}

// TaintInfo 描述一个对象实例的污染态。
type TaintInfo struct {
	ObjectID     string
	EarliestStep int
	Entry        uint64
}

// Outcome 是一次动作执行（含可能的补偿）的完整判定结果。
type Outcome struct {
	ActionID string
	// Category 最终判定分类（固定优先级取最高者）。
	Category Category
	Reason   string
	// FailedStep 导致失败的子操作编号；-1 表示全部成功。
	FailedStep int

	Applied      []StepEffect
	Compensated  []int
	CompFailures []CompRecord
	Tainted      []TaintInfo

	// RejectedBeforeEffect 为 true 表示未对对象图做任何可观察改动
	// （污染拒绝 / 争用拒绝 / 业务拒绝均属此类）。
	RejectedBeforeEffect bool
}

// Tracer 接收每次动作与补偿的逐步事件，用于打印与对拍。
type Tracer interface {
	ActionStart(actionID string, opCount int)
	StepApplied(actionID string, step int, entry uint64, detail string)
	StepRejected(actionID string, step int, category Category, reason string)
	CompStart(actionID string, count int)
	CompStep(actionID string, rec CompRecord)
	CompDone(actionID string)
	ActionDone(actionID string, category Category, reason string)
}
