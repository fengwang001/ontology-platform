package ontology

// HookFunc 是前置校验钩子。
// 它只能读事务状态；返回非空 reason 表示拒绝该 Action。
// 钩子内任何写操作都不会生效，并会使整个 Action 在提交时失败。
type HookFunc func(t *Txn, args map[string]any) (reason string)

// HandlerFunc 是 Action 的业务逻辑。
// 它在事务内执行，可以创建/修改对象、建立/删除关系，
// 也可以通过 t.Call 嵌套调用其它 Action。返回 error 则事务回滚。
type HandlerFunc func(t *Txn, args map[string]any) error

// ActionType 声明一个业务动作：参数契约、触碰的对象类型、
// 有序前置钩子与业务处理器。
type ActionType struct {
	Name        string
	Schema      Schema
	ObjectTypes []string
	Hooks       []HookFunc
	Handler     HandlerFunc

	// defaultSnapshot 保存声明时默认值，确保每次调用拿到的
	// 默认值都与最初声明一致，不被历史调用污染。
	defaultSnapshot map[string]any
}

// NewActionType 创建 ActionType 并冻结默认值快照。
func NewActionType(name string, sc Schema, objectTypes []string,
	hooks []HookFunc, handler HandlerFunc) *ActionType {
	a := &ActionType{
		Name:        name,
		Schema:      sc,
		ObjectTypes: objectTypes,
		Hooks:       hooks,
		Handler:     handler,
	}
	snap := make(map[string]any)
	for _, p := range sc.Params {
		if p.Default != nil {
			snap[p.Name] = deepCopy(p.Default)
		}
	}
	a.defaultSnapshot = snap
	return a
}
