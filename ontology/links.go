package ontology

// Cardinality 描述链接类型 From:To 方向的基数约束。
type Cardinality string

const (
	OneToOne   Cardinality = "ONE_TO_ONE"
	OneToMany  Cardinality = "ONE_TO_MANY"
	ManyToOne  Cardinality = "MANY_TO_ONE"
	ManyToMany Cardinality = "MANY_TO_MANY"
)

// CardinalityVersion 是基数约束的一个版本，有效期为 [From, To)。
type CardinalityVersion struct {
	Interval
	Card Cardinality
}

// LinkID 由 (类型, 起点, 终点) 唯一确定一条链接。
type LinkID struct {
	Type string
	From string
	To   string
}

// linkHistory 是单条链接的存在性区间序列（按 From 升序）。
// 切片一旦发布即不可变；新增/关闭区间通过整体替换发布。
type linkHistory struct {
	intervals []Interval
}

// existsAt 报告该链接在版本 v 时是否存在，并返回二分比较次数。
// 比较次数只取决于这一条链接自身的创建/撤销次数，
// 与链接类型累计的创建撤销历史总量无关。
func (lh *linkHistory) existsAt(v Version) (bool, int) {
	idx, steps := findInterval(lh.intervals, v)
	return idx >= 0, steps
}

// linkTypeState 是某链接类型的全部版本化状态。
type linkTypeState struct {
	fromType string
	toType   string
	cards    []CardinalityVersion // 按 From 升序，不可变发布
	links    map[LinkID]*linkHistory
	// out 是 From 对象 -> 候选链接 的邻接索引（按 LinkID 字典序有序）。
	out map[string][]LinkID
}

// cardinalityAt 返回恰好覆盖版本 v 的基数约束版本。
func (lt *linkTypeState) cardinalityAt(v Version) (*CardinalityVersion, int, bool) {
	ivs := make([]Interval, len(lt.cards))
	for i, c := range lt.cards {
		ivs[i] = c.Interval
	}
	idx, steps := findInterval(ivs, v)
	if idx < 0 {
		return nil, steps, false
	}
	return &lt.cards[idx], steps, true
}
