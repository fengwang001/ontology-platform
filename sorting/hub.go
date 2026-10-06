package sorting

import (
	"sort"
	"sync"
)

// Hub 快递中转场。所有方法可并发调用，效果等价于某个串行顺序；
// 相同操作序列重放得到完全相同的集袋编号、封袋时机与差异清单。
//
// 复杂度保证：加入快件的判定只依赖
//   - openBySite：目的网点 -> 开放集袋（map 查找）；
//   - inField：场内运单号 -> 所在集袋（map 查找）；
//   - 开放袋的聚合计数（件数/总重/品类标记，随加入 O(1) 维护）。
//
// 不扫描历史集袋与历史快件，开销与二者总量无关。
type Hub struct {
	mu   sync.RWMutex
	cfg  Config
	last int64 // 上一次被接受操作的时刻；被拒绝的操作不推进时钟

	nextBagID  int64
	bags       map[int64]*bag  // 全部集袋（含历史）
	openBySite map[string]*bag // 每个目的网点至多一个开放集袋
	inField    map[string]int64
	// 场内运单号 -> 所在集袋。已加入且尚未被拆袋确认的快件视为在场；
	// 拆袋确认（扫到且在袋内）后离场，缺失件仍留在场内（待查）。
}

// NewHub 创建中转场。配置非法时返回参数非法错误。
func NewHub(cfg Config) (*Hub, error) {
	if cfg.MaxBagCount < 1 {
		return nil, errInvalidParam("件数上限必须 >= 1，实际 %d", cfg.MaxBagCount)
	}
	if cfg.MaxBagWeight < 1 {
		return nil, errInvalidParam("总重上限必须 >= 1，实际 %d", cfg.MaxBagWeight)
	}
	if cfg.DwellLimit < 0 {
		return nil, errInvalidParam("停留时限必须 >= 0，实际 %d", cfg.DwellLimit)
	}
	return &Hub{
		cfg:        cfg,
		nextBagID:  1,
		bags:       make(map[int64]*bag),
		openBySite: make(map[string]*bag),
		inField:    make(map[string]int64),
	}, nil
}

// checkClock 仅校验时钟回退，不推进时钟。
// 调用方必须持有写锁，且所有参数校验已通过（参数非法优先于时钟回退）。
// 时钟只在操作被接受、即将提交状态时由 commitClock 推进，
// 从而保证被拒绝的操作不改变任何状态、编号与时钟。
func (h *Hub) checkClock(now int64) error {
	if now < h.last {
		return errClockRollback(now, h.last)
	}
	return nil
}

// commitClock 在操作被接受时推进时钟。
func (h *Hub) commitClock(now int64) {
	h.last = now
}

// AddParcel 将快件分拣入袋，返回所在集袋编号。
// 判定失败时不改变任何状态、编号与时钟。
func (h *Hub) AddParcel(p Parcel, now int64) (int64, error) {
	// 1. 参数非法
	if p.Waybill == "" {
		return 0, errInvalidParam("运单号为空")
	}
	if p.Site == "" {
		return 0, errInvalidParam("目的网点编码为空")
	}
	if p.Weight < 1 || p.Weight > MaxParcelWeight {
		return 0, errInvalidParam("重量 %d 超出合法范围 [1, %d]", p.Weight, MaxParcelWeight)
	}
	if !p.Category.Valid() {
		return 0, errInvalidParam("品类非法: %d", int(p.Category))
	}
	if now < 0 {
		return 0, errInvalidParam("时刻必须为非负整数，实际 %d", now)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// 2. 时钟回退
	if err := h.checkClock(now); err != nil {
		return 0, err
	}

	// 6. 业务拒绝：重复 / 单件超重
	if _, ok := h.inField[p.Waybill]; ok {
		return 0, errBusinessReject("运单号 %s 已在场内，重复加入", p.Waybill)
	}
	if p.Weight > h.cfg.MaxBagWeight {
		return 0, errBusinessReject("单件重量 %d 超过集袋总重上限 %d，无法分拣", p.Weight, h.cfg.MaxBagWeight)
	}

	// 通过全部校验，执行分拣（此后不再失败）。
	h.commitClock(now)
	b, ok := h.openBySite[p.Site]
	if ok && !b.canAccept(p, h.cfg, now) {
		// 先封存旧袋再新开一袋：与放入构成同一原子操作（同一把锁内完成）。
		b.status = BagSealed
		b.sealTime = now
		delete(h.openBySite, p.Site)
		ok = false
	}
	if !ok {
		b = newBag(h.nextBagID, p.Site, now)
		h.nextBagID++
		h.bags[b.id] = b
		h.openBySite[p.Site] = b
	}
	b.add(p)
	h.inField[p.Waybill] = b.id
	return b.id, nil
}

// SealBag 手动封存某网点的开放集袋，返回集袋编号。
func (h *Hub) SealBag(site string, now int64) (int64, error) {
	if site == "" {
		return 0, errInvalidParam("目的网点编码为空")
	}
	if now < 0 {
		return 0, errInvalidParam("时刻必须为非负整数，实际 %d", now)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.checkClock(now); err != nil {
		return 0, err
	}
	b, ok := h.openBySite[site]
	if !ok {
		return 0, errNotFound("网点 %s 无开放集袋", site)
	}
	h.commitClock(now)
	b.status = BagSealed
	b.sealTime = now
	delete(h.openBySite, site)
	return b.id, nil
}

// DepartBag 已封存的集袋随指定车次出场。
func (h *Hub) DepartBag(bagID int64, trainNo string, now int64) error {
	if bagID < 1 {
		return errInvalidParam("集袋编号必须为正整数，实际 %d", bagID)
	}
	if trainNo == "" {
		return errInvalidParam("车次为空")
	}
	if now < 0 {
		return errInvalidParam("时刻必须为非负整数，实际 %d", now)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.checkClock(now); err != nil {
		return err
	}
	b, ok := h.bags[bagID]
	if !ok {
		return errNotFound("集袋 %d 不存在", bagID)
	}
	if b.status != BagSealed {
		return errStateMismatch("集袋 %d 状态为 %s，仅已封存的集袋可出场", bagID, b.status)
	}
	h.commitClock(now)
	b.status = BagDeparted
	b.departTime = now
	b.trainNo = trainNo
	return nil
}

// UnpackBag 目的网点拆袋并核对实际扫描到的运单号集合。
// 一个集袋只能拆袋一次；重复拆袋报状态不符且不改变既有差异记录。
func (h *Hub) UnpackBag(bagID int64, site string, scanned []string, now int64) (UnpackResult, error) {
	if bagID < 1 {
		return UnpackResult{}, errInvalidParam("集袋编号必须为正整数，实际 %d", bagID)
	}
	if site == "" {
		return UnpackResult{}, errInvalidParam("目的网点编码为空")
	}
	if now < 0 {
		return UnpackResult{}, errInvalidParam("时刻必须为非负整数，实际 %d", now)
	}
	seen := make(map[string]bool, len(scanned))
	for _, w := range scanned {
		if w == "" {
			return UnpackResult{}, errInvalidParam("扫描集合含空运单号")
		}
		if seen[w] {
			return UnpackResult{}, errInvalidParam("扫描集合含重复运单号 %s", w)
		}
		seen[w] = true
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.checkClock(now); err != nil {
		return UnpackResult{}, err
	}
	b, ok := h.bags[bagID]
	if !ok {
		return UnpackResult{}, errNotFound("集袋 %d 不存在", bagID)
	}
	if b.status != BagDeparted {
		return UnpackResult{}, errStateMismatch("集袋 %d 状态为 %s，仅已出场的集袋可拆袋", bagID, b.status)
	}
	if b.site != site {
		return UnpackResult{}, errSiteMismatch("集袋 %d 目的网点为 %s，与给定网点 %s 不一致", bagID, b.site, site)
	}

	h.commitClock(now)
	// 核对：缺失 = 袋内有而未扫到；多出 = 扫到而不在袋内。均按运单号升序。
	var missing, extra []string
	for _, w := range b.waybills {
		if !seen[w] {
			missing = append(missing, w)
		}
	}
	for w := range seen {
		if !b.inBag[w] {
			extra = append(extra, w)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	// 扫到且在袋内的快件确认离场；缺失件标记待查，仍视为在场。
	for _, w := range b.waybills {
		if seen[w] {
			delete(h.inField, w)
		}
	}

	b.status = BagUnpacked
	b.unpackTime = now
	b.unpackSite = site
	b.missing = missing
	b.extra = extra
	return UnpackResult{BagID: bagID, Site: site, Missing: missing, Extra: extra}, nil
}

// OpenBag 查询某网点当前的开放集袋；无开放集袋时 ok=false。
func (h *Hub) OpenBag(site string) (BagInfo, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	b, ok := h.openBySite[site]
	if !ok {
		return BagInfo{}, false
	}
	return b.snapshot(), true
}

// BagInfo 按集袋编号查询内容与状态快照。
func (h *Hub) BagInfo(bagID int64) (BagInfo, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	b, ok := h.bags[bagID]
	if !ok {
		return BagInfo{}, false
	}
	return b.snapshot(), true
}

// BagOfWaybill 按运单号查询所在集袋编号；不在场内时 ok=false。
func (h *Hub) BagOfWaybill(waybill string) (int64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	id, ok := h.inField[waybill]
	return id, ok
}

// LastAcceptedTime 返回上一次被接受操作的时刻（只读）。
func (h *Hub) LastAcceptedTime() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.last
}
