package ontology

// memberSnap 是某版本提交时刻对某成员的判定快照：
// 一旦写入不再变化，延期授予/撤销均不回溯。
type memberSnap struct {
	effDeadline int64   // 该成员在该版本提交时刻的有效截止
	late        int64   // 提交时刻 - 有效截止（<=0 为准时）
	penalty     float64 // 按迟交时长取档得到的扣分比例
	invalid     bool    // 迟交时长超过最后一档上限
}

// version 是一次提交产生的版本。onTime/invalid 为主体级判定
// （小组主体只看小组延期），成员级扣分见 snaps。
type version struct {
	number    int
	time      int64
	submitter string
	onTime    bool
	invalid   bool // 主体级迟交时长超过最后一档上限：仍记录但不得被选为评分版本
	snaps     map[string]memberSnap
}

// subject 是一个提交主体的版本序列与显式指定。
// 版本号从 1 开始连续无洞，被拒绝的提交不消耗版本号。
type subject struct {
	id         string
	versions   []*version
	designated int // 显式指定的评分版本号，0 表示未指定
}

func newSubject(id string) *subject {
	return &subject{id: id}
}

// findTier 返回首个「时长上限不小于 late」的档位；late 超过最后一档上限时 ok=false。
// 二分查找，开销只随档位数增长。
func findTier(tiers []Tier, late int64) (t Tier, ok bool) {
	t, ok, _ = findTierCounted(tiers, late)
	return t, ok
}

// findTierCounted 同 findTier，但返回比较次数，供测试验证复杂度上界。
func findTierCounted(tiers []Tier, late int64) (Tier, bool, int) {
	comparisons := 0
	lo, hi := 0, len(tiers) // 答案在 [lo, hi) 中，hi 为「超过最后一档」
	for lo < hi {
		mid := lo + (hi-lo)/2
		comparisons++
		if tiers[mid].MaxLate >= late {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo == len(tiers) {
		return Tier{}, false, comparisons
	}
	return tiers[lo], true, comparisons
}
