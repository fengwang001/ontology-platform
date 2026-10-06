package slotting

import (
	"io"
	"sort"
)

// System 是货位分配系统的线程安全外观。
// 单一读写互斥保证所有操作可线性化：写操作串行等价；读操作取写锁生成快照副本，
// 因此读不到任何“移库进行到一半”的中间状态。
type System struct {
	mu
	st     *store
	logger *opLogger
}

// NewSystem 创建空系统；log 非 nil 时打印输入/输出/判定依据。
func NewSystem(log io.Writer) *System {
	return &System{st: newStore(), logger: newLogger(log)}
}

// AddLocation 登记货位。重复登记报重复。
func (s *System) AddLocation(loc Location) (err error) {
	s.Lock()
	defer s.Unlock()
	defer func() {
		if err != nil {
			s.logger.emitf("AddLocation 输入=%s 输出=拒绝 依据=%v", loc.ID, err)
		} else {
			s.logger.emitf("AddLocation 输入=%s 输出=成功", loc.ID)
		}
	}()
	return s.addLocationLocked(loc)
}

// RegisterPallet 登记托盘档案（不等于上架）。
func (s *System) RegisterPallet(p Pallet) (err error) {
	s.Lock()
	defer s.Unlock()
	defer func() {
		if err != nil {
			s.logger.emitf("RegisterPallet 输入=%s 输出=拒绝 依据=%v", p.ID, err)
		} else {
			s.logger.emitf("RegisterPallet 输入=%s 输出=成功", p.ID)
		}
	}()
	return s.registerPalletLocked(p)
}

// AutoPlace 自动上架，返回所选货位。
func (s *System) AutoPlace(palletID string) (id LocationID, err error) {
	s.Lock()
	defer s.Unlock()
	id, err = s.autoPlaceLocked(palletID)
	if err != nil {
		s.logger.emitf("AutoPlace 输入=%s 输出=拒绝 依据=%v 考察货位=%d", palletID, err, s.st.examined)
		return LocationID{}, err
	}
	s.logger.emitf("AutoPlace 输入=%s 输出=%s 依据=同类优先/空位字典序最小 考察货位=%d", palletID, id, s.st.examined)
	return id, nil
}

// PlaceTo 指定货位上架。
func (s *System) PlaceTo(palletID string, target LocationID) (err error) {
	s.Lock()
	defer s.Unlock()
	err = s.placeToLocked(palletID, target)
	s.logger.emitf("PlaceTo 输入=(%s -> %s) 输出=%v", palletID, target, resultText(err))
	return err
}

// Move 移库；失败两侧均不变。
func (s *System) Move(palletID string, target LocationID) (err error) {
	s.Lock()
	defer s.Unlock()
	err = s.moveLocked(palletID, target)
	s.logger.emitf("Move 输入=(%s -> %s) 输出=%v", palletID, target, resultText(err))
	return err
}

// TakeOut 取出托盘并释放占用。
func (s *System) TakeOut(palletID string) (from LocationID, err error) {
	s.Lock()
	defer s.Unlock()
	from, err = s.takeOutLocked(palletID)
	s.logger.emitf("TakeOut 输入=%s 输出=%v", palletID, resultText(err))
	return from, err
}

// Freeze 冻结货位：已有托盘保留，只可取出/移出，不可再放入。
func (s *System) Freeze(id LocationID) (err error) {
	s.Lock()
	defer s.Unlock()
	err = s.setStatusLocked(id, StatusFrozen)
	s.logger.emitf("Freeze 输入=%s 输出=%v", id, resultText(err))
	return err
}

// Unfreeze 解冻货位。
func (s *System) Unfreeze(id LocationID) (err error) {
	s.Lock()
	defer s.Unlock()
	err = s.setStatusLocked(id, StatusNormal)
	s.logger.emitf("Unfreeze 输入=%s 输出=%v", id, resultText(err))
	return err
}

// BatchAutoPlace 按序批量自动上架，全有或全无；返回失败下标。
func (s *System) BatchAutoPlace(palletIDs []string) (failedIndex int, err error) {
	s.Lock()
	defer s.Unlock()
	ids := append([]string(nil), palletIDs...)
	failedIndex, err = s.batchAutoPlaceLocked(ids)
	if err != nil {
		s.logger.emitf("BatchAutoPlace 输入%v 输出=整批拒绝 失败下标=%d 依据=%v", ids, failedIndex, err)
		return failedIndex, err
	}
	s.logger.emitf("BatchAutoPlace 输入%v 输出=全部成功", ids)
	return -1, nil
}

// GetLocation 返回某货位在位托盘与剩余承重的一致快照。
func (s *System) GetLocation(id LocationID) (LocationView, error) {
	s.RLock()
	defer s.RUnlock()
	ls, ok := s.st.locations[id]
	if !ok {
		return LocationView{}, fail(ReasonLocationNotFound, "货位 %s 不存在", id)
	}
	pids := append([]string(nil), ls.pallets...)
	return LocationView{
		ID:             ls.loc.ID,
		Status:         ls.loc.Status,
		Capacity:       ls.loc.Capacity,
		UsedCapacity:   len(ls.pallets),
		RemainCapacity: ls.loc.Capacity - len(ls.pallets),
		MaxWeight:      ls.loc.MaxWeight,
		UsedWeight:     ls.weight,
		RemainWeight:   ls.loc.MaxWeight - ls.weight,
		ClearHeight:    ls.loc.ClearHeight,
		PalletIDs:      pids,
	}, nil
}

// PalletLocation 返回某托盘当前所在货位；未上架返回错误。
func (s *System) PalletLocation(palletID string) (LocationID, error) {
	s.RLock()
	defer s.RUnlock()
	if _, ok := s.st.pallets[palletID]; !ok {
		return LocationID{}, fail(ReasonPalletNotFound, "托盘 %s 不存在", palletID)
	}
	id, ok := s.st.where[palletID]
	if !ok {
		return LocationID{}, fail(ReasonPalletNotFound, "托盘 %s 尚未上架", palletID)
	}
	return id, nil
}

// ProductLocations 返回某商品所有在位货位（字典序）。
func (s *System) ProductLocations(product string) []LocationID {
	s.RLock()
	defer s.RUnlock()
	ids := s.st.productSortedIDs(product)
	sort.Slice(ids, func(i, j int) bool { return ids[i].Less(ids[j]) })
	return ids
}

// ExaminedCount 返回最近一次自动上架（或批量中的最后一次尝试）实际考察的货位数，
// 供“考察货位数次线性”的可验证证明使用。
func (s *System) ExaminedCount() int {
	s.RLock()
	defer s.RUnlock()
	return s.st.examined
}

func resultText(err error) string {
	if err == nil {
		return "成功"
	}
	return "拒绝: " + err.Error()
}
