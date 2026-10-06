package ontology

// StmtKind 标识语句形态。
type StmtKind int

const (
	StmtAssign StmtKind = iota
	StmtIf
	StmtReturn
)

// CondKind 标识条件形态。
type CondKind int

const (
	CondIsType CondKind = iota // Is: typeof（空值判定为对象）
	CondEqNull                 // 与空值严格相等
	CondEqUndefined
	CondEqLiteral // 与字面量严格相等
	CondLooseNull // 与空值宽松相等（同时命中空值与未定义）
	CondTruthy    // 变量自身真值判断
	CondDiscrim   // 判别属性：属性与字面量严格相等
	CondNot
	CondAnd
	CondOr
)

// CheckedType 是 typeof 式判断可检查的类型。
type CheckedType int

const (
	CheckNumber CheckedType = iota
	CheckString
	CheckBoolean
	CheckObject
	CheckUndefined
)

// Cond 描述一个条件。各 CondKind 使用的字段见文档。
type Cond struct {
	K     CondKind
	Var   string
	Check CheckedType
	Lit   *Type // 字面量类型（CondEqLiteral / CondDiscrim）
	Prop  string
	Inner *Cond
	Left  *Cond
	Right *Cond
}

// Stmt 是一条语句。
type Stmt struct {
	ID    string
	K     StmtKind
	Var   string
	Value *Type
	Cond  *Cond
	Then  []*Stmt
	Else  []*Stmt
}

// Program 是变量声明与语句序列。
type Program struct {
	Decls map[string]*Type
	Stmts []*Stmt
}
