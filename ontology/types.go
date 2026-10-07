// Package ontology 实现本体平台的对象实例存储，核心特性为属性级（而非整实例级）
// 的并发写入冲突判定。
package ontology

// RemoteRead 描述校验钩子声明的跨实例读取范围：
// 经由某链接类型指向 TargetType 类型实例的 Prop 属性。
// 归属裁定仅依据钩子声明，与链接是否实际存在无关。
type RemoteRead struct {
	// Link 是声明该读取关系所经由的链接类型名（仅作来源记录，不参与裁定）。
	Link string
	// TargetType 是被指向实例所属的对象类型名。
	TargetType string
	// Prop 是被读取的目标实例属性名。
	Prop string
}

// ValidationHook 是注册在对象类型上的校验钩子。
// 钩子对每次写入声明其读取字段集合，该集合并入冲突判定的"相关读取集合"。
type ValidationHook struct {
	Name string
	// ReadProps 是钩子对每次写入都声明读取的本实例属性名集合。
	ReadProps []string
	// RemoteReads 是钩子对每次写入都声明读取的被指向实例属性集合。
	RemoteReads []RemoteRead
	// DeclareReads 可选：按本次写入的写集合动态声明额外的读取字段，
	// 使"为本次写入声明过的读取字段集合"可以随写入内容变化。
	DeclareReads func(writeSet map[string]struct{}) (local []string, remote []RemoteRead)
	// Validate 可选：在冲突判定通过后，对合并后的预期状态执行校验。
	// 返回非 nil 错误时写入被拒绝（ErrKindValidation）。
	Validate func(values map[string]string) error
}

// ObjectType 描述一个对象类型及其注册的校验钩子。
type ObjectType struct {
	Name  string
	Hooks []ValidationHook
}

// LinkType 描述对象类型之间的链接类型。
// 链接的存在本身不构成冲突裁定的依据，仅为钩子声明提供来源语境。
type LinkType struct {
	Name       string
	SourceType string
	TargetType string
}

// WriteRequest 是一次写入请求。
type WriteRequest struct {
	ObjectID string
	// Baseline 是调用方声明的预期起点版本号，仅用于确定预期起点；
	// 实际冲突判定以提交时刻的最新已提交版本为准。
	Baseline uint64
	// Set 是要写入的属性键值对，其键集合构成本次写入的写集合。
	Set map[string]string
}

// WriteResult 是一次成功写入的结果。
type WriteResult struct {
	ObjectID   string
	Version    uint64
	DecisionID uint64
}
