package ontology

// FactKind 双时态事实种类。
type FactKind int

const (
	// FactCreate 链接创建：声明链接自 ValidFrom 起在有效时间轴上成立。
	FactCreate FactKind = iota
	// FactRevoke 链接撤销：关闭该链接当前开放的有效时间区间（止于 ValidTo）。
	FactRevoke
	// FactCorroborate 对称链接对端补录确认：消除单端记录造成的对称性缺损标记。
	FactCorroborate
)

// Endpoint 事实的记录来源端点。该信息仅供内部维护对称性缺损判定，
// 绝不会出现在任何审计输出中（见 ReplayResult / AuditReport，均无此字段）。
type Endpoint int

const (
	EndpointLeft Endpoint = iota
	EndpointRight
	// EndpointBoth 表示系统级规范记录（常规写入路径），双端同时生效。
	EndpointBoth
)

// Fact 一条双时态历史事实。RecordedAt 为记录时间轴坐标（由全局序号给出），
// ValidFrom/ValidTo 为有效时间轴坐标。事实一旦写入即不可变。
type Fact struct {
	Seq        int64
	RecordedAt int64
	Kind       FactKind
	Left       ObjectID // 规范化后的左端（对称链接按字典序排序）
	Right      ObjectID
	ValidFrom  int64
	ValidTo    int64 // 仅 FactRevoke 使用
	source     Endpoint
}

// pair 链接的规范化键。对称链接中两端已按字典序排列，
// 因此 (a,b) 与 (b,a) 映射到同一键，天然保证双向可见。
type pair struct {
	L ObjectID
	R ObjectID
}

// canonicalPair 按链接类型的对称性将两端规范化为唯一键。
func canonicalPair(def LinkTypeDef, a, b ObjectID) pair {
	if def.Symmetric && b < a {
		return pair{L: b, R: a}
	}
	return pair{L: a, R: b}
}

// interval 有效时间区间 [From, To)，To == openEnd 表示仍然开放。
type interval struct {
	From int64
	To   int64
}

func (iv interval) covers(vt int64) bool {
	return iv.From <= vt && vt < iv.To
}

// pairState 单个链接对在某个记录时刻的内部状态（不可变值，写时复制）。
type pairState struct {
	ivs     []interval
	deficit bool // 对称链接存在单端记录缺陷（缺少对端 corroboration）
}

// copy 返回 pairState 的深拷贝（区间切片独立）。
func (ps pairState) copy() pairState {
	ivs := make([]interval, len(ps.ivs))
	copy(ivs, ps.ivs)
	return pairState{ivs: ivs, deficit: ps.deficit}
}

// open 返回当前开放区间的下标，无开放区间时返回 -1。
func (ps pairState) open() int {
	for i := len(ps.ivs) - 1; i >= 0; i-- {
		if ps.ivs[i].To == openEnd {
			return i
		}
	}
	return -1
}

// applyFact 将一条事实作用于链接状态表（调用方需保证已做写时复制）。
// 返回被触及的链接对。该函数是提交与回放共用的唯一状态转移定义，
// 保证两条路径语义一致。
func applyFact(states map[pair]*pairState, f Fact, def LinkTypeDef) pair {
	p := pair{L: f.Left, R: f.Right}
	ps, ok := states[p]
	if !ok {
		ps = &pairState{}
		states[p] = ps
	}
	switch f.Kind {
	case FactCreate:
		if ps.open() < 0 {
			ps.ivs = append(ps.ivs, interval{From: f.ValidFrom, To: openEnd})
		}
		// 对称链接：系统级规范记录（EndpointBoth）无缺损；
		// 单端记录在对端补录确认之前标记为对称性缺损。
		if def.Symmetric {
			ps.deficit = f.source != EndpointBoth
		}
	case FactCorroborate:
		ps.deficit = false
	case FactRevoke:
		if i := ps.open(); i >= 0 {
			ps.ivs[i].To = f.ValidTo
		}
		// 无开放区间的撤销为悬空撤销：不改动状态，但保留在事实日志中。
	}
	return p
}
