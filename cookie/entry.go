package cookie

// SameSite 是条目的同站模式。
type SameSite string

const (
	// SameSiteStrict 严格模式：仅同站请求附带。
	SameSiteStrict SameSite = "strict"
	// SameSiteLax 宽松模式：同站，或顶层导航的安全方法。
	SameSiteLax SameSite = "lax"
	// SameSiteNone 无模式：不做同站限制，但必须仅安全。
	SameSiteNone SameSite = "none"
)

// Key 是条目的身份键：名字 + 站点 + 路径 + 分区键。
type Key struct {
	Name      string
	Domain    string
	Path      string
	Partition string
}

// Entry 是对外暴露的条目快照（不含内部堆索引）。
type Entry struct {
	Name       string
	Value      string
	Domain     string
	Path       string
	Secure     bool
	HTTPOnly   bool
	SameSite   SameSite
	Expires    *int64 // nil 表示会话级
	Partition  string
	CreatedAt  int64
	LastAccess int64
}

func (e *entry) snapshot() Entry {
	return Entry{
		Name:       e.key.Name,
		Value:      e.value,
		Domain:     e.key.Domain,
		Path:       e.key.Path,
		Secure:     e.secure,
		HTTPOnly:   e.httpOnly,
		SameSite:   e.sameSite,
		Expires:    clonePtr(e.expires),
		Partition:  e.key.Partition,
		CreatedAt:  e.createdAt,
		LastAccess: e.lastAccess,
	}
}

func clonePtr(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// entry 是存储内部条目，带各堆的定位索引（-1 表示不在对应堆中）。
type entry struct {
	key        Key
	value      string
	secure     bool
	httpOnly   bool
	sameSite   SameSite
	expires    *int64
	createdAt  int64
	lastAccess int64

	lruIndex    int // 所属站点 LRU 堆
	expiryIndex int // 所属站点过期堆（会话级为 -1）
	gblExpIndex int // 全局过期堆
}

// WriteInput 是一次结构化写入的输入。
type WriteInput struct {
	Name         string
	Value        string
	Domain       string
	Path         string
	Secure       bool
	HTTPOnly     bool
	SameSite     SameSite // 空串表示未显式声明（按宽松+宽限处理）
	Expires      *int64
	Partition    string
	SourceDomain string // 写入来源站点
	SourceSecure bool   // 写入来源是否安全
	Now          int64
}

// RequestInput 是一次请求附带判定的输入。
type RequestInput struct {
	TargetDomain string
	TargetPath   string
	Secure       bool
	Initiator    string // 发起者站点，可为空
	TopNav       bool
	SafeMethod   bool
	Partition    string
	Now          int64
}

// Evaluation 记录单个候选条目是否附带及判定依据，用于日志与测试。
type Evaluation struct {
	Entry  Entry
	Send   bool
	Reason string
}

// RequestResult 是附带判定结果。
type RequestResult struct {
	Sent        []Entry
	Evaluations []Evaluation
}

// ClearInput 是清除操作输入；未设置的维度不作为过滤条件。
type ClearInput struct {
	Domain          string
	Partition       string
	UsePartition    bool
	CreatedFrom     int64
	CreatedTo       int64
	UseCreatedRange bool
	Now             int64
}

// Stats 是可区分查询的删除统计与容量快照。
type Stats struct {
	TotalAdded      int64 // 累计成功新增条目数（覆盖不计）
	CurrentCount    int   // 当前条目总数
	ExpiryEvicted   int64 // 过期淘汰
	CapacityEvicted int64 // 容量淘汰
	ExplicitDelete  int64 // 显式删除（覆盖、过期写入、清除）
	Now             int64
}

// Config 是内核的可配置参数。
type Config struct {
	PerSiteLimit int   // 每站点条目数上限
	GlobalLimit  int   // 全局条目总数上限
	LaxGrace     int64 // 未声明同站模式的宽限时长（>=0）
}

// Logger 打印输入、输出与判定依据；nil 时使用默认标准日志实现。
type Logger interface {
	Printf(format string, args ...any)
}
