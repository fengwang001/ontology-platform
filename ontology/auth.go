package ontology

// Authorizer 是权限判定接口。引擎对每次判定计数，
// 使得“权限检查次数只与动作实际声明与实际触及的实例数量相关”
// 成为可观测、可断言的性质。
type Authorizer interface {
	// Visible 报告主体是否能看到该实例（存在性级别的可见性）。
	Visible(subject SubjectID, inst InstanceID) bool
	// Authorize 给出主体在指定传播层级（depth，0 为直接目标层）
	// 对该实例的授权结论。不同层级可以给出不同结论。
	Authorize(subject SubjectID, inst InstanceID, depth int) bool
}

// AuthorizerFunc 用一对函数构造 Authorizer，便于测试与接入。
type AuthorizerFunc struct {
	VisibleFn   func(subject SubjectID, inst InstanceID) bool
	AuthorizeFn func(subject SubjectID, inst InstanceID, depth int) bool
}

// Visible 实现 Authorizer。
func (a AuthorizerFunc) Visible(s SubjectID, id InstanceID) bool { return a.VisibleFn(s, id) }

// Authorize 实现 Authorizer。
func (a AuthorizerFunc) Authorize(s SubjectID, id InstanceID, d int) bool {
	return a.AuthorizeFn(s, id, d)
}

// CountingAuthorizer 包装另一个 Authorizer 并统计判定调用次数。
// 计数是“开销不随系统总规模增长”这一性质的可观测证明手段。
type CountingAuthorizer struct {
	Inner        Authorizer
	VisibleCalls int
	AuthCalls    int
	CheckedInsts map[InstanceID]bool
}

// NewCountingAuthorizer 构造计数包装器。
func NewCountingAuthorizer(inner Authorizer) *CountingAuthorizer {
	return &CountingAuthorizer{Inner: inner, CheckedInsts: map[InstanceID]bool{}}
}

// Visible 实现 Authorizer 并计数。
func (c *CountingAuthorizer) Visible(s SubjectID, id InstanceID) bool {
	c.VisibleCalls++
	c.CheckedInsts[id] = true
	return c.Inner.Visible(s, id)
}

// Authorize 实现 Authorizer 并计数。
func (c *CountingAuthorizer) Authorize(s SubjectID, id InstanceID, d int) bool {
	c.AuthCalls++
	c.CheckedInsts[id] = true
	return c.Inner.Authorize(s, id, d)
}

// Total 返回判定调用总次数。
func (c *CountingAuthorizer) Total() int { return c.VisibleCalls + c.AuthCalls }
