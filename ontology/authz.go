package ontology

// Decider 是主体对某实例在某传播深度上的授权判定来源。
// 深度即传播路径层级：直接目标为深度 0。
type Decider interface {
	// Visible 表示主体是否能感知该实例存在。不可见时不得返回其它属性。
	Visible(subject SubjectID, inst Instance, depth int) bool
	// Allowed 表示主体在该深度层级对该操作是否放行。
	Allowed(subject SubjectID, inst Instance, op Operation, depth int) (allowed, abstain bool)
}
