package ontology

import "fmt"

// BoundKind 描述一侧基数上限的形态。
type BoundKind int

const (
	// Unlimited 未声明上限，该侧不做基数限制。
	Unlimited BoundKind = iota
	// ExactlyOne 恰好一个：上限 1。
	ExactlyOne
	// AtMostOne 至多一个：上限 1。
	AtMostOne
	// AtMost 至多 Max 个具体正整数上限。
	AtMost
)

// CardinalityBound 是链接类型一端对象类型一侧的基数上限声明。
type CardinalityBound struct {
	Kind BoundKind
	Max  int
}

// LinkTypeDecl 声明一个链接类型及其两端基数。
type LinkTypeDecl struct {
	Name       string
	SourceType string
	TargetType string
	SourceCap  CardinalityBound
	TargetCap  CardinalityBound
}

// StoredLink 是链接存储中的一条具体链接。
type StoredLink struct {
	LinkType string
	Pair
}

// Pair 是一条有序实例对。
type Pair struct {
	Source string
	Target string
}

// Cap 返回该侧声明的基数上限；ok 为 false 表示无限制。
//
// ExactlyOne 与 AtMostOne 都归一化为上限 1。语义差异（ExactlyOne 要求恰好存在）
// 属于链接集合完整性约束，通常在对象生命周期收口处校验；创建路径上二者同为
// 「不得超过 1」，本系统对创建/批量导入只实施上限约束。
func (b CardinalityBound) Cap() (cap int, limited bool) {
	switch b.Kind {
	case ExactlyOne, AtMostOne:
		return 1, true
	case AtMost:
		if b.Max < 1 {
			panic(fmt.Sprintf("ontology: AtMost 上限必须是正整数，得到 %d", b.Max))
		}
		return b.Max, true
	default:
		return 0, false
	}
}

// Validate 在声明注册时做一次结构性校验，避免非法声明进入运行期。
func (d LinkTypeDecl) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("ontology: 链接类型名称不能为空")
	}
	if d.SourceType == "" || d.TargetType == "" {
		return fmt.Errorf("ontology: 链接类型 %s 的两端对象类型不能为空", d.Name)
	}
	if d.SourceCap.Kind == AtMost && d.SourceCap.Max < 1 {
		return fmt.Errorf("ontology: 链接类型 %s 起点上限必须是正整数", d.Name)
	}
	if d.TargetCap.Kind == AtMost && d.TargetCap.Max < 1 {
		return fmt.Errorf("ontology: 链接类型 %s 终点上限必须是正整数", d.Name)
	}
	return nil
}
