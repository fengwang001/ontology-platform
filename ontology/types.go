// Package ontology 提供本体平台的核心领域类型与批量导入的属性级权限守门器。
package ontology

// ImportMode 声明一次批量导入的权限处理模式。
type ImportMode string

const (
	// ModeAtomic 原子模式：记录内任一字段无权限则整条记录失败。
	ModeAtomic ImportMode = "atomic"
	// ModeLenient 宽松模式：无权限字段被跳过，其余字段正常写入。
	ModeLenient ImportMode = "lenient"
)

// RecordSemantic 声明单条记录的写入语义。
type RecordSemantic string

const (
	// SemanticCreate 创建语义：目标对象必须不存在。
	SemanticCreate RecordSemantic = "create"
	// SemanticUpdate 更新语义：目标对象必须已存在。
	SemanticUpdate RecordSemantic = "update"
)

// FailureCategory 可区分的失败类别。
type FailureCategory string

const (
	// FailBatchInvalidParam 整批级：模式参数缺失或非法，或发起主体不存在。
	FailBatchInvalidParam FailureCategory = "batch_invalid_param"
	// FailTypeNotFound 记录级：对象类型不存在。
	FailTypeNotFound FailureCategory = "type_not_found"
	// FailSemanticMismatch 记录级：创建/更新语义与对象存在性不匹配。
	FailSemanticMismatch FailureCategory = "semantic_mismatch"
	// FailPermissionDenied 记录级：字段权限拒绝（原子模式整条拒绝）。
	FailPermissionDenied FailureCategory = "permission_denied"
	// FailRequiredConstraint 记录级：跳过后必需属性约束不满足。
	FailRequiredConstraint FailureCategory = "required_constraint"
)

// RecordStatus 单条记录的最终判定。
type RecordStatus string

const (
	StatusSuccess RecordStatus = "success"
	StatusPartial RecordStatus = "partial_success"
	StatusFailed  RecordStatus = "failed"
)

// ObjectType 对象类型定义，RequiredProps 为必需属性约束。
type ObjectType struct {
	Name          string
	RequiredProps []string
}

// Object 对象实例，Props 保存属性名到值的映射。
type Object struct {
	TypeName string
	ID       string
	Props    map[string]string
}

// PermissionEntry 一条属性级写权限条目。
type PermissionEntry struct {
	Subject    string
	ObjectType string
	Property   string
	Writable   bool
}

// Record 批量导入中的单条记录。
type Record struct {
	ObjectType string
	ObjectID   string
	Semantic   RecordSemantic
	Fields     map[string]string
}

// RecordResult 单条记录的判定结果。
type RecordResult struct {
	Index        int
	Status       RecordStatus
	Skipped      []string
	FailCategory FailureCategory
	Reason       string
}

// BatchRequest 一次批量导入请求。
type BatchRequest struct {
	Mode    ImportMode
	Subject string
	Records []Record
}

// BatchResult 批量导入总体结果。
type BatchResult struct {
	Rejected     bool
	RejectReason string
	Results      []RecordResult
}

// DecisionLog 一次判定的结构化日志：输入、输出与依据。
type DecisionLog struct {
	Scope  string // "batch" 或 "record"
	Index  int    // 记录下标，批级为 -1
	Input  string
	Output string
	Basis  string
}

// Logger 判定日志输出接口。
type Logger interface {
	LogDecision(entry DecisionLog)
}
