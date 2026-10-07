package ontology

// linkSig 描述一种链接允许的一组（源类型, 目标类型）配对。
type linkSig struct {
	srcType string
	dstType string
}

// object 是一个对象实例。
type object struct {
	id    string
	typ   string
	attrs map[string]int64
}

// reachKey 标识一个（起点, 终点）对。
type reachKey struct {
	start string
	end   string
}

// aggInfo 是某起点实例的聚合维护状态。
type aggInfo struct {
	present bool
	value   int64
	maxEnd  string // 当前最大值来源；并列时任选一个
}

// snapshot 是单个视图在一次维护过程中可以整体提交/回滚的可变状态。
type snapshot struct {
	// reach：（起点,终点）之间的路径条数（>0 即可达）。
	reach map[reachKey]int64
	// agg：每个起点实例的聚合结果。
	agg map[string]*aggInfo
	// endRefs：终点 -> 当前能到达它的起点集合。
	endRefs map[string]map[string]struct{}
	// rescanCost：重定最大值来源时考察过的终点实例总数。
	rescanCost int
}

// viewState 是已注册聚合视图的全部维护状态。
type viewState struct {
	spec ViewSpec
	// cycleExcludable 表示能否在声明阶段静态排除环（各层类型集合两两不相交）。
	cycleExcludable bool
	// snap 为 nil 表示当前有一次未完成的维护（用于注入失败时回滚）。
	snap *snapshot
	// failNext 为 true 时下一次增量维护在提交前注入失败（测试用）。
	failNext bool
}
