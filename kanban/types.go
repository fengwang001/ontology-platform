package kanban

// LimitUnlimited 表示列在制品上限不限。
const LimitUnlimited = 0

// Card 是看板卡片的快照。
type Card struct {
	ID      string
	Owner   string
	Column  int // 列下标，0 为待办，len(columns)-1 为完成
	Version int
	// Expedited 为 true 表示卡片当前处于进行中区域且持有唯一加急标记。
	Expedited bool
	// Prereqs 是该卡片的全部前置卡片 ID（AddDep 声明的有向边 prerequisite -> card），已排序。
	Prereqs []string
}

// Result 是一次被接受操作的结果快照。
type Result struct {
	Card     Card
	Now      int64
	Accepted bool
}
