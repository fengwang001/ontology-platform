package slotting

import "sync"

type mu = sync.RWMutex

// 校验辅助 ----------------------------------------------------------------

func validCategory(c Category) bool { return c == CatGeneral || c == CatFood || c == CatFlammable }

func validateLocation(l Location) error {
	id := l.ID
	if id.Aisle <= 0 || id.Level <= 0 || id.Index <= 0 {
		return fail(ReasonInvalidArgument, "货位坐标必须为正整数: %s", id)
	}
	if l.MaxWeight <= 0 || l.ClearHeight <= 0 {
		return fail(ReasonInvalidArgument, "承重上限与净高必须为正整数")
	}
	if l.Capacity != 1 && l.Capacity != 2 {
		return fail(ReasonInvalidArgument, "容纳托盘数必须为 1 或 2")
	}
	if l.Status != "" && l.Status != StatusNormal && l.Status != StatusFrozen {
		return fail(ReasonInvalidArgument, "货位状态非法: %s", l.Status)
	}
	if len(l.Allowed) == 0 {
		return fail(ReasonInvalidArgument, "货位 %s 至少允许一个品类", id)
	}
	seen := map[Category]bool{}
	for _, c := range l.Allowed {
		if !validCategory(c) {
			return fail(ReasonInvalidArgument, "未知品类: %s", c)
		}
		if seen[c] {
			return fail(ReasonInvalidArgument, "品类集合重复: %s", c)
		}
		seen[c] = true
	}
	return nil
}

func validatePallet(p Pallet) error {
	if p.ID == "" || p.Product == "" || p.Batch == "" {
		return fail(ReasonInvalidArgument, "托盘编号、商品、批次不能为空")
	}
	if !validCategory(p.Category) {
		return fail(ReasonInvalidArgument, "未知品类: %s", p.Category)
	}
	if p.Weight <= 0 || p.Height <= 0 {
		return fail(ReasonInvalidArgument, "托盘 %s 的重量与高度必须为正整数", p.ID)
	}
	return nil
}

// 写操作（调用方持写锁） -----------------------------------------------------

func (s *System) addLocationLocked(loc Location) error {
	if err := validateLocation(loc); err != nil {
		return err
	}
	if _, ok := s.st.locations[loc.ID]; ok {
		return fail(ReasonDuplicate, "货位 %s 已存在", loc.ID)
	}
	if loc.Status == "" {
		loc.Status = StatusNormal
	}
	s.st.addLocation(loc)
	return nil
}

func (s *System) registerPalletLocked(p Pallet) error {
	if err := validatePallet(p); err != nil {
		return err
	}
	if _, ok := s.st.pallets[p.ID]; ok {
		return fail(ReasonDuplicate, "托盘编号 %s 已注册", p.ID)
	}
	s.st.pallets[p.ID] = &p
	return nil
}

// autoPlaceLocked 自动选择货位。
// 第一类候选：该商品已有在位托盘且仍有剩余容量的货位，按字典序逐个全约束检查；
// 第二类候选：空货位，经分块索引定位字典序最小可行货位。
func (s *System) autoPlaceLocked(palletID string) (LocationID, error) {
	p, ok := s.st.pallets[palletID]
	if !ok {
		return LocationID{}, fail(ReasonPalletNotFound, "托盘 %s 不存在", palletID)
	}
	if _, placed := s.st.where[palletID]; placed {
		return LocationID{}, fail(ReasonDuplicate, "托盘 %s 已在货位上", palletID)
	}
	examined := 0
	for _, id := range s.st.productSortedIDs(p.Product) {
		ls := s.st.locations[id]
		if len(ls.pallets) >= ls.loc.Capacity {
			continue
		}
		examined++
		if err := checkPlace(s.st, ls, p, true); err == nil {
			s.st.examined = examined
			s.st.place(id, p)
			return id, nil
		}
	}
	// 第二类候选：空货位分块索引（skyline 已覆盖相邻隔离）。
	id, n := s.st.empty.firstFeasible(p.Category, p.Height, p.Weight)
	examined += n
	s.st.examined = examined
	if id == (LocationID{}) {
		return LocationID{}, fail(ReasonNoAvailableLocation,
			"托盘 %s（商品 %s, 品类 %s, 重 %d, 高 %d）无可用货位",
			p.ID, p.Product, p.Category, p.Weight, p.Height)
	}
	ls := s.st.locations[id]
	if err := checkPlace(s.st, ls, p, true); err != nil {
		return LocationID{}, err // 索引与检查不一致（理论不可达）
	}
	s.st.place(id, p)
	return id, nil
}

func (s *System) placeToLocked(palletID string, target LocationID) error {
	p, ok := s.st.pallets[palletID]
	if !ok {
		return fail(ReasonPalletNotFound, "托盘 %s 不存在", palletID)
	}
	ls, ok := s.st.locations[target]
	if !ok {
		return fail(ReasonLocationNotFound, "货位 %s 不存在", target)
	}
	if _, placed := s.st.where[palletID]; placed {
		return fail(ReasonDuplicate, "托盘 %s 已在货位上", palletID)
	}
	if err := checkPlace(s.st, ls, p, true); err != nil {
		return err
	}
	s.st.place(target, p)
	return nil
}

// moveLocked 在克隆快照上完成“离开原位→放入目标”，任一步失败原状态不变。
func (s *System) moveLocked(palletID string, target LocationID) error {
	if _, ok := s.st.pallets[palletID]; !ok {
		return fail(ReasonPalletNotFound, "托盘 %s 不存在", palletID)
	}
	origin, ok := s.st.where[palletID]
	if !ok {
		return fail(ReasonPalletNotFound, "托盘 %s 尚未上架", palletID)
	}
	if origin == target {
		return fail(ReasonSelfLocation, "货位 %s 即托盘 %s 原位", target, palletID)
	}
	if _, ok := s.st.locations[target]; !ok {
		return fail(ReasonLocationNotFound, "货位 %s 不存在", target)
	}

	c := s.st.clone()
	p := c.pallets[palletID]
	if !c.release(origin, palletID) {
		return fail(ReasonPalletNotFound, "托盘 %s 不在台账记录的货位 %s", palletID, origin)
	}
	if err := checkPlace(c, c.locations[target], p, true); err != nil {
		return err // 原状态不变（c 将被丢弃）
	}
	c.place(target, p)
	s.st = c
	return nil
}

func (s *System) takeOutLocked(palletID string) (LocationID, error) {
	id, ok := s.st.where[palletID]
	if !ok {
		if _, reg := s.st.pallets[palletID]; !reg {
			return LocationID{}, fail(ReasonPalletNotFound, "托盘 %s 不存在", palletID)
		}
		return LocationID{}, fail(ReasonPalletNotFound, "托盘 %s 尚未上架", palletID)
	}
	if !s.st.release(id, palletID) {
		return LocationID{}, fail(ReasonPalletNotFound, "托盘 %s 台账不一致", palletID)
	}
	return id, nil
}

func (s *System) setStatusLocked(id LocationID, status LocationStatus) error {
	ls, ok := s.st.locations[id]
	if !ok {
		return fail(ReasonLocationNotFound, "货位 %s 不存在", id)
	}
	if ls.loc.Status == status {
		return nil
	}
	s.st.setStatus(id, status)
	return nil
}

// batchAutoPlaceLocked 逐托盘按序分配；任一失败整体无变化。
func (s *System) batchAutoPlaceLocked(ids []string) (failedIndex int, err error) {
	if len(ids) == 0 {
		return 0, fail(ReasonInvalidArgument, "批量列表为空")
	}
	seen := map[string]struct{}{}
	for i, id := range ids {
		if id == "" {
			return i, fail(ReasonInvalidArgument, "第 %d 个托盘编号为空", i)
		}
		if _, dup := seen[id]; dup {
			return i, fail(ReasonInvalidArgument, "批内托盘编号重复: %s（下标 %d）", id, i)
		}
		seen[id] = struct{}{}
	}
	saved := s.st.clone()
	for i, id := range ids {
		if _, e := s.autoPlaceLocked(id); e != nil {
			s.st = saved // 整批回滚，不留痕
			return i, e
		}
	}
	return -1, nil
}
