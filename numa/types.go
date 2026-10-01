package numa

// Policy 决定提示合并后的准入策略。
type Policy string

const (
	PolicyNone           Policy = "none"
	PolicyBestEffort     Policy = "best-effort"
	PolicyRestricted     Policy = "restricted"
	PolicySingleNUMANode Policy = "single-numa-node"
)

// Hint 是一个 NUMA 亲和提示：Mask 为非空节点集合，Preferred 表示偏好。
type Hint struct {
	Mask      uint64
	Preferred bool
}

// ProviderHints 描述一个外部提供者的回答：
// nil 或 NoPreference==true 表示无偏好；Hints 为空列表表示无法满足。
type ProviderHints struct {
	NoPreference bool
	Hints        []Hint
}

// Result 是一次成功 Admit 的结果。
type Result struct {
	Mask       uint64
	Preferred  bool
	Allocation []int64
}
