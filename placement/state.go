package placement

import "sync"

// filterMu 是过滤器的全部可变状态，统一受锁保护。
type filterMu struct {
	sync.Mutex

	// Q 为预留数量上限。
	Q int
	// nodes 为节点名 -> 可用区。
	nodes map[string]string
	// pods 为已放置（已提交或预留中）的 Pod，按 ID 索引。
	pods map[string]*placedPod
	// reserved 为预留中的 Pod ID 集合。
	reserved map[string]struct{}
}

// placedPod 是一个已放置的 Pod。
type placedPod struct {
	pod      Pod
	node     string
	reserved bool
}
