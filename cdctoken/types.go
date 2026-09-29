package cdctoken

// Column 表示一行镜像中的一列。同一镜像内列名必须唯一。
// Value 为 nil 表示 SQL NULL；空字符串 "" 是普通值。
type Column struct {
	Name  string
	Value any
}

// Row 是变更前或变更后的行镜像，按列存储；列顺序不影响结果。
type Row struct {
	Columns []Column
}

// Event 是一个变更数据捕获事件。Before 为 nil 表示插入；
// After 为 nil 表示删除；两者皆非 nil 表示更新。
type Event struct {
	Table string
	Before *Row
	After  *Row
}

// Config 描述脱敏规则与令牌表上限。
type Config struct {
	// Domains 为已注册的域名集合，不能为空串。
	Domains []string
	// Tables 为每张已注册表配置敏感列与其所属域。
	Tables map[string]TableConfig
	// MaxTokensPerDomain 限制每个域最多可分配的令牌数；0 表示不限制。
	MaxTokensPerDomain int
}

// TableConfig 描述一张表的敏感列。未列出的列原样透传。
type TableConfig struct {
	SensitiveColumns map[string]string
}

// Logger 记录处理过程中的输入、令牌与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// Tokenizer 是并发安全的确定性令牌化脱敏器。
type Tokenizer struct {
	// 字段在实现阶段填充。
}

// New 依据配置创建脱敏器，配置非法时返回 ReasonInvalidConfig。
func New(cfg Config) (*Tokenizer, error) {
	return nil, nil
}

// WithLogger 返回一个使用给定日志器记录过程的脱敏器。
func (t *Tokenizer) WithLogger(logger Logger) *Tokenizer { return t }

// ProcessEvent 对事件做脱敏，返回一个全新的事件，不修改入参事件。
// 任何非法输入都会被整体拒绝，且不改变令牌表。
func (t *Tokenizer) ProcessEvent(event Event) (Event, error) {
	return Event{}, nil
}

// TokenCount 返回指定域当前已分配的令牌数量。
func (t *Tokenizer) TokenCount(domain string) int { return 0 }

// Token 返回指定域内原值当前对应的令牌；原值尚未分配时返回空串与 false。
func (t *Tokenizer) Token(domain, value string) (string, bool) { return "", false }

// NextNumber 返回指定域下一个将被分配的编号。
func (t *Tokenizer) NextNumber(domain string) int { return 1 }

