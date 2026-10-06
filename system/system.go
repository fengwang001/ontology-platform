// Package system 实现特种设备检验周期与超期管控系统的核心服务。
//
// 职责：维护对象（设备与附件）的生命周期状态、到期日、挂接关系，
// 执行登记/检验/封存/启封/挂接/摘除/使用登记/报废等操作，并提供
// 可使用性判定与预警查询。所有操作在全局读写锁下执行，并发调用
// 等价于某个串行顺序；被拒绝的操作不改变任何状态与时钟。
//
// 索引成员约定：到期索引仅包含状态为“在用”的对象（封存、停用、
// 报废对象不在索引中），因此预警查询的扫描量严格正比于命中条数。
package system

import (
	"fmt"
	"sync"

	"ontology/domain"
	"ontology/index"
)

// Object 是受控对象（设备或附件）的内部表示。
type Object struct {
	ID         string
	Cat        domain.Category
	Status     domain.Status
	Expiry     int             // 到期日（日序号），有效期含当日
	HostID     string          // 附件：所挂设备编号；空表示未挂接
	Attach     map[string]bool // 设备：当前挂接的附件编号集合
	SealAnchor int             // 封存计时锚点：封存当日，或封存期内最近一次检验日
}

// overdue 报告对象在 date 当日是否已超期（到期日次日起为超期）。
func (o *Object) overdue(date int) bool { return date > o.Expiry }

// WarnEntry 是预警结果条目。
type WarnEntry struct {
	ID     string          // 对象编号
	Cat    domain.Category // 对象类别
	Expiry int             // 对象自身到期日
	Via    []string        // 设备被附件触发时，触发的附件编号（升序）；否则为空
}

// System 是管控系统核心。零值不可用，须用 New 构造。
type System struct {
	mu       sync.RWMutex
	configs  map[domain.Category]domain.Config
	objects  map[string]*Object
	idx      *index.Index
	lastDate int  // 上一个被接受操作的日期
	hasDate  bool // 是否已有被接受操作
}

// New 构造系统。configs 须覆盖全部四个类别且各字段合法。
func New(configs map[domain.Category]domain.Config) (*System, error) {
	for _, cat := range []domain.Category{
		domain.CatBoiler, domain.CatPressureVessel, domain.CatSafetyValve, domain.CatPressureGauge,
	} {
		cfg, ok := configs[cat]
		if !ok {
			return nil, domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("缺少类别配置: %s", cat))
		}
		if !cfg.Valid() {
			return nil, domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("类别 %s 配置非法", cat))
		}
	}
	cp := make(map[domain.Category]domain.Config, len(configs))
	for k, v := range configs {
		cp[k] = v
	}
	return &System{configs: cp, objects: make(map[string]*Object), idx: index.New()}, nil
}

// checkClock 校验日期回退；仅在变更操作通过参数校验后调用。
func (s *System) checkClock(date int) error {
	if s.hasDate && date < s.lastDate {
		return domain.NewError(domain.ErrDateRegression,
			fmt.Sprintf("日期 %d 小于上一个被接受操作的日期 %d", date, s.lastDate))
	}
	return nil
}

// advanceClock 在接受变更后推进时钟。
func (s *System) advanceClock(date int) {
	s.lastDate = date
	s.hasDate = true
}

// get 查找对象；不存在时报 ErrNotFound，已报废时报 ErrScrapped。
func (s *System) get(id string) (*Object, error) {
	obj, ok := s.objects[id]
	if !ok {
		return nil, domain.NewError(domain.ErrNotFound, "对象不存在: "+id)
	}
	if obj.Status == domain.StatusScrapped {
		return nil, domain.NewError(domain.ErrScrapped, "对象已报废: "+id)
	}
	return obj, nil
}
