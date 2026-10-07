package ontology

// Kind 区分审计记录是一次动作，还是一条订正记录。
type Kind int

const (
	// KindAction 一次动作执行（提交或回退）。
	KindAction Kind = iota
	// KindCorrection 一条追加在某序号之后的订正记录。
	KindCorrection
)

func (k Kind) String() string {
	switch k {
	case KindAction:
		return "ACTION"
	case KindCorrection:
		return "CORRECTION"
	default:
		return "UNKNOWN"
	}
}

// Outcome 是一次动作 / 订正的执行结果。
type Outcome int

const (
	// Committed 提交生效。
	Committed Outcome = iota
	// RolledBack 整体回退，仅表示「发生过一次尝试」。
	RolledBack
)

func (o Outcome) String() string {
	switch o {
	case Committed:
		return "COMMITTED"
	case RolledBack:
		return "ROLLED_BACK"
	default:
		return "UNKNOWN"
	}
}

// Change 是单个实例在一次动作中的变更前后值。
type Change struct {
	Instance string
	Before   string
	After    string
}

// Record 是审计序列中的一条不可篡改记录。
type Record struct {
	Seq          int
	ActionID     string
	Kind         Kind
	Outcome      Outcome
	Changes      []Change
	CorrectionOf int
	PrevHash     string
	Hash         string
}

// ChangeEvent 是重放区间内观察到的一条序列事件。
type ChangeEvent struct {
	Seq     int
	Kind    Kind
	Outcome Outcome
	Action  *ActionChange
}

// ActionChange 是一次动作对单个实例造成的（订正后视图下的）有效变更。
type ActionChange struct {
	Instance string
	Before   string
	After    string
}
