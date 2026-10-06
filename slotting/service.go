package slotting

// Service 对外提供上架、移库、冻结与查询等操作。
// 所有写操作在单一互斥临界区内完成"校验 + 记账 + 索引更新"，
// 因而结果等价于某个串行顺序，且不存在半完成状态。
type Service struct {
	store *Store
}

// NewService 创建服务。
func NewService(store *Store) *Service {
	return &Service{store: store}
}

// Store 暴露存储以便注册货位。
func (svc *Service) Store() *Store { return svc.store }

// AutoPutaway 自动上架：优先同商品且有余位的候选，其次空货位；
// 均按 (通道号, 层号, 位序号) 字典序取最小者。
func (svc *Service) AutoPutaway(p Pallet) (Coord, error) {
	if err := validatePallet(p); err != nil {
		return Coord{}, err
	}
	s := svc.store
	s.lock()
	defer s.unlock()

	if _, ok := s.where[p.ID]; ok {
		return Coord{}, errf(ReasonDuplicate, "pallet %s already stored", p.ID)
	}
	c, reason := svc.autoPickLocked(p)
	if reason != "" {
		return Coord{}, &SlotError{Reason: reason}
	}
	loc, _ := s.loc(c)
	s.place(loc, p)
	return c, nil
}

// autoPickLocked 在候选索引上按规则选位。
// 仅遍历：① 该商品的同商品候选集合；② 全局空货位集合。
// 货位是否被检查只与"该商品的候选"与"空货位"有关，与其他商品在位托盘数、
// 与货位总数（非空且非本商品候选的部分）均无关。返回原因用于区分失败。
func (svc *Service) autoPickLocked(p Pallet) (Coord, Reason) {
	c, r, _ := svc.autoPickCountLocked(p)
	return c, r
}

// autoPickCountLocked 与 autoPickLocked 行为一致，额外返回被实际考察的货位数。
// 考察范围只包含：该商品的同商品候选集合 + 全局空货位集合，
// 不遍历其他商品占用的货位，也不遍历全部货位。
func (svc *Service) autoPickCountLocked(p Pallet) (Coord, Reason, int) {
	s := svc.store
	examined := 0
	if set := s.byProduct[p.Product]; set != nil {
		var keys []Coord
		keys = set.ascending(keys)
		for _, c := range keys {
			examined++
			loc := s.locs[c]
			if checkPut(loc, p, s.neighbors(c)).reason() == "" {
				return c, "", examined
			}
		}
	}
	var keys []Coord
	keys = s.empties.ascending(keys)
	for _, c := range keys {
		examined++
		loc := s.locs[c]
		if checkPut(loc, p, s.neighbors(c)).reason() == "" {
			return c, "", examined
		}
	}
	return Coord{}, ReasonNoLocation, examined
}

// PutawayTo 指定货位上架，按优先级返回首个拒绝原因。
func (svc *Service) PutawayTo(p Pallet, target Coord) error {
	if err := validatePallet(p); err != nil {
		return err
	}
	if target.Aisle <= 0 || target.Level <= 0 || target.Position <= 0 {
		return errf(ReasonInvalidParam, "target coord components must be positive")
	}
	s := svc.store
	s.lock()
	defer s.unlock()

	if _, ok := s.where[p.ID]; ok {
		return errf(ReasonDuplicate, "pallet %s already stored", p.ID)
	}
	loc, ok := s.loc(target)
	if !ok {
		return &SlotError{Reason: ReasonLocationAbsent}
	}
	if r := checkPut(loc, p, s.neighbors(target)).reason(); r != "" {
		return &SlotError{Reason: r}
	}
	s.place(loc, p)
	return nil
}

// Move 把托盘从原货位移到目标货位；目标约束按"不含该托盘自身原位"计算。
func (svc *Service) Move(palletID string, target Coord) error {
	if palletID == "" {
		return errf(ReasonInvalidParam, "pallet id empty")
	}
	if target.Aisle <= 0 || target.Level <= 0 || target.Position <= 0 {
		return errf(ReasonInvalidParam, "target coord components must be positive")
	}
	s := svc.store
	s.lock()
	defer s.unlock()

	src, ok := s.where[palletID]
	if !ok {
		return &SlotError{Reason: ReasonPalletMissing}
	}
	if src == target {
		return &SlotError{Reason: ReasonSameLocation}
	}
	dst, ok := s.loc(target)
	if !ok {
		return &SlotError{Reason: ReasonLocationAbsent}
	}
	srcLoc := s.locs[src]
	p := s.remove(srcLoc, palletID)
	if r := checkPut(dst, p, s.neighbors(target)).reason(); r != "" {
		// 回滚：原货位立即恢复，两侧不变。
		s.place(srcLoc, p)
		return &SlotError{Reason: r}
	}
	s.place(dst, p)
	return nil
}

// Retrieve 取出托盘并释放占用。
func (svc *Service) Retrieve(palletID string) error {
	if palletID == "" {
		return errf(ReasonInvalidParam, "pallet id empty")
	}
	s := svc.store
	s.lock()
	defer s.unlock()

	c, ok := s.where[palletID]
	if !ok {
		return &SlotError{Reason: ReasonPalletMissing}
	}
	s.remove(s.locs[c], palletID)
	return nil
}

// Freeze / Unfreeze 切换货位冻结状态；重复设置幂等。
func (svc *Service) Freeze(c Coord) error {
	return svc.setStatus(c, true)
}

func (svc *Service) Unfreeze(c Coord) error {
	return svc.setStatus(c, false)
}

func (svc *Service) setStatus(c Coord, frozen bool) error {
	if c.Aisle <= 0 || c.Level <= 0 || c.Position <= 0 {
		return errf(ReasonInvalidParam, "coord components must be positive")
	}
	s := svc.store
	s.lock()
	defer s.unlock()
	if _, ok := s.loc(c); !ok {
		return &SlotError{Reason: ReasonLocationAbsent}
	}
	want := StatusNormal
	if frozen {
		want = StatusFrozen
	}
	if s.locs[c].config.Status != want {
		s.setFrozen(c, frozen)
	}
	return nil
}

// BatchPutaway 按给定次序逐托盘自动分配；任一失败整批无变化。
// 结果 map 为 托盘编号 -> 货位。
func (svc *Service) BatchPutaway(pallets []Pallet) (map[string]Coord, error) {
	if len(pallets) == 0 {
		return map[string]Coord{}, nil
	}
	seen := map[string]int{}
	for i, p := range pallets {
		if err := validatePallet(p); err != nil {
			return nil, &BatchError{Index: i, Err: err}
		}
		if j, dup := seen[p.ID]; dup {
			return nil, &BatchError{
				Index: j, // 编号首次出现的下标即批内重复的最小下标
				Err:   errf(ReasonInvalidParam, "duplicate pallet id in batch: %s", p.ID),
			}
		}
		seen[p.ID] = i
	}

	s := svc.store
	s.lock()
	defer s.unlock()

	// 批外已存在的编号也算重复（首个失败下标为准）。
	for i, p := range pallets {
		if _, ok := s.where[p.ID]; ok {
			return nil, &BatchError{Index: i, Err: errf(ReasonDuplicate, "pallet %s already stored", p.ID)}
		}
	}

	type done struct {
		p   Pallet
		loc *locEntry
	}
	placed := make([]done, 0, len(pallets))
	for i, p := range pallets {
		c, reason := svc.autoPickLocked(p)
		if reason != "" {
			// 逆序回滚，账目恢复到批前状态。
			for k := len(placed) - 1; k >= 0; k-- {
				s.remove(placed[k].loc, placed[k].p.ID)
			}
			return nil, &BatchError{Index: i, Err: &SlotError{Reason: reason}}
		}
		loc := s.locs[c]
		s.place(loc, p)
		placed = append(placed, done{p: p, loc: loc})
	}
	result := make(map[string]Coord, len(pallets))
	for _, d := range placed {
		result[d.p.ID] = d.loc.config.Coord
	}
	return result, nil
}

// BatchError 标出批内最小失败下标与可区分原因。
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string { return e.Err.Error() }
func (e *BatchError) Unwrap() error { return e.Err }

// ---- 查询：持读锁构造快照副本后释放，绝不返回内部可变引用 ----

// Location 返回某货位的一致快照。
func (svc *Service) Location(c Coord) (LocationView, error) {
	if c.Aisle <= 0 || c.Level <= 0 || c.Position <= 0 {
		return LocationView{}, errf(ReasonInvalidParam, "coord components must be positive")
	}
	s := svc.store
	s.rlock()
	defer s.runlock()
	loc, ok := s.loc(c)
	if !ok {
		return LocationView{}, &SlotError{Reason: ReasonLocationAbsent}
	}
	return snapshot(loc), nil
}

func snapshot(loc *locEntry) LocationView {
	cats := make([]Category, 0, len(loc.config.Allowed))
	for cat := range loc.config.Allowed {
		cats = append(cats, cat)
	}
	ids := make([]string, 0, len(loc.pallets))
	for _, p := range loc.pallets {
		ids = append(ids, p.ID)
	}
	return LocationView{
		Coord:           loc.config.Coord,
		WeightLimit:     loc.config.WeightLimit,
		ClearHeight:     loc.config.ClearHeight,
		Allowed:         cats,
		Capacity:        loc.config.Capacity,
		Occupied:        len(loc.pallets),
		AllowMixedBatch: loc.config.AllowMixedBatch,
		Status:          loc.config.Status,
		PalletIDs:       ids,
		RemainingWeight: loc.config.WeightLimit - loc.weight,
	}
}

// PalletLocation 查询托盘所在货位。
func (svc *Service) PalletLocation(palletID string) (Coord, error) {
	if palletID == "" {
		return Coord{}, errf(ReasonInvalidParam, "pallet id empty")
	}
	s := svc.store
	s.rlock()
	defer s.runlock()
	c, ok := s.where[palletID]
	if !ok {
		return Coord{}, &SlotError{Reason: ReasonPalletMissing}
	}
	return c, nil
}

// ProductLocations 查询某商品当前所在货位集合（字典序）。
func (svc *Service) ProductLocations(product string) []Coord {
	s := svc.store
	s.rlock()
	defer s.runlock()
	set := s.productAll[product]
	if set == nil {
		return []Coord{}
	}
	return set.ascending(nil)
}
