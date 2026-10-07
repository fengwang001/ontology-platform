package ontology

// accInfo 接触累计信息：正时长重叠的累计分钟数、最早起点与最晚结束时刻。
type accInfo struct {
	minutes int64
	first   int64 // 首个正时长重叠的起点
	last    int64 // 最晚重叠区间的结束时刻
}

// secInfo 次密接认定信息。
type secInfo struct {
	minutes int64
	last    int64
	via     string // 使其达标的密切接触者
}

// caseRec 一个病例的登记数据与推导缓存。
// 缓存只是“当前全部登记 + now”的纯函数记忆化，可随时重建，
// 因此查询结果不依赖旧的推导结果。
type caseRec struct {
	id           string
	patient      string
	onset        int64 // 发病时刻（可被改正）
	registeredAt int64 // 确诊登记时刻（登记操作的 now，之后不变）
	isolated     bool
	isolatedAt   int64
	revoked      bool

	// 推导缓存
	valid        bool
	derivedAt    int64
	nowSensitive bool // 推导读取了开区间且病例未隔离：now 前进可能改变结果
	close        map[string]accInfo
	secondary    map[string]secInfo
}

// infectiousStart 传染期起点：发病前 48 小时（含），下限为 0。
func (c *caseRec) infectiousStart() int64 {
	if c.onset < InfectiousLeadMinutes {
		return 0
	}
	return c.onset - InfectiousLeadMinutes
}

// caseStore 病例存储：按 ID 与按患者索引。
type caseStore struct {
	byID      map[string]*caseRec
	byPatient map[string][]*caseRec
}

func newCaseStore() *caseStore {
	return &caseStore{
		byID:      make(map[string]*caseRec),
		byPatient: make(map[string][]*caseRec),
	}
}

func (cs *caseStore) get(id string) *caseRec { return cs.byID[id] }

func (cs *caseStore) ofPatient(p string) []*caseRec { return cs.byPatient[p] }

func (cs *caseStore) add(c *caseRec) {
	cs.byID[c.id] = c
	cs.byPatient[c.patient] = append(cs.byPatient[c.patient], c)
}
