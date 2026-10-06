// Package naive 是管控系统的朴素对照模型：不维护任何索引，
// 所有查询都全表扫描，逻辑尽量直白，用于在随机操作序列下
// 与正式实现（system 包）做差分对照。
package naive

import (
	"fmt"
	"sort"

	"ontology/calendar"
	"ontology/domain"
	"ontology/system"
)

// Object 是朴素模型中的对象。
type Object struct {
	ID         string
	Cat        domain.Category
	Status     domain.Status
	Expiry     int
	HostID     string
	Attach     map[string]bool
	SealAnchor int
}

// Naive 是朴素模型。
type Naive struct {
	configs  map[domain.Category]domain.Config
	objects  map[string]*Object
	lastDate int
	hasDate  bool
}

// New 构造朴素模型。
func New(configs map[domain.Category]domain.Config) *Naive {
	return &Naive{configs: configs, objects: make(map[string]*Object)}
}

func (n *Naive) clock(date int) error {
	if n.hasDate && date < n.lastDate {
		return domain.NewError(domain.ErrDateRegression,
			fmt.Sprintf("日期 %d 小于上一个被接受操作的日期 %d", date, n.lastDate))
	}
	return nil
}

func (n *Naive) accept(date int) { n.lastDate, n.hasDate = date, true }

func (n *Naive) get(id string) (*Object, error) {
	obj, ok := n.objects[id]
	if !ok {
		return nil, domain.NewError(domain.ErrNotFound, "对象不存在: "+id)
	}
	if obj.Status == domain.StatusScrapped {
		return nil, domain.NewError(domain.ErrScrapped, "对象已报废: "+id)
	}
	return obj, nil
}

func overdue(expiry, date int) bool { return date > expiry }

// Register 登记新对象。
func (n *Naive) Register(id string, cat domain.Category, firstPassDate int) error {
	if id == "" || !cat.Valid() || firstPassDate < 0 {
		return domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	if err := n.clock(firstPassDate); err != nil {
		return err
	}
	if _, ok := n.objects[id]; ok {
		return domain.NewError(domain.ErrInvalidParam, "编号已存在: "+id)
	}
	obj := &Object{
		ID:     id,
		Cat:    cat,
		Status: domain.StatusInService,
		Expiry: calendar.AddMonths(firstPassDate, n.configs[cat].PeriodMonths),
	}
	if cat.IsDevice() {
		obj.Attach = make(map[string]bool)
	}
	n.objects[id] = obj
	n.accept(firstPassDate)
	return nil
}

// Inspect 检验。
func (n *Naive) Inspect(id string, date int, result domain.InspectResult, rectDays int) error {
	if id == "" || date < 0 {
		return domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	switch result {
	case domain.ResultPass, domain.ResultFail:
		if rectDays != 0 {
			return domain.NewError(domain.ErrInvalidParam, "非有条件合格时整改限期天数须为 0")
		}
	case domain.ResultConditional:
		if rectDays <= 0 {
			return domain.NewError(domain.ErrInvalidParam, "有条件合格须附带正的整改限期天数")
		}
	default:
		return domain.NewError(domain.ErrInvalidParam, "未知检验结论")
	}
	if err := n.clock(date); err != nil {
		return err
	}
	obj, err := n.get(id)
	if err != nil {
		return err
	}
	cfg := n.configs[obj.Cat]
	if result == domain.ResultFail {
		obj.Status = domain.StatusSuspended
		obj.SealAnchor = 0
		n.accept(date)
		return nil
	}
	var base int
	if date >= obj.Expiry-cfg.EarlyWindowDays && date <= obj.Expiry {
		base = obj.Expiry
	} else {
		base = date
	}
	newExpiry := calendar.AddMonths(base, cfg.PeriodMonths)
	if result == domain.ResultConditional && date+rectDays < newExpiry {
		newExpiry = date + rectDays
	}
	obj.Expiry = newExpiry
	if obj.Status == domain.StatusSuspended {
		obj.Status = domain.StatusInService
	} else if obj.Status == domain.StatusSealed {
		obj.SealAnchor = date
	}
	n.accept(date)
	return nil
}

// Seal 封存。
func (n *Naive) Seal(id string, date int) error {
	if id == "" || date < 0 {
		return domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	if err := n.clock(date); err != nil {
		return err
	}
	obj, err := n.get(id)
	if err != nil {
		return err
	}
	if obj.Status != domain.StatusInService {
		return domain.NewError(domain.ErrStateNotAllowed, "仅在用对象可封存")
	}
	if overdue(obj.Expiry, date) {
		return domain.NewError(domain.ErrConditionUnmet, "对象已超期，不得封存")
	}
	obj.Status = domain.StatusSealed
	obj.SealAnchor = date
	n.accept(date)
	return nil
}

// Unseal 启封。
func (n *Naive) Unseal(id string, date int) error {
	if id == "" || date < 0 {
		return domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	if err := n.clock(date); err != nil {
		return err
	}
	obj, err := n.get(id)
	if err != nil {
		return err
	}
	if obj.Status != domain.StatusSealed {
		return domain.NewError(domain.ErrStateNotAllowed, "仅封存对象可启封")
	}
	newExpiry := obj.Expiry + (date - obj.SealAnchor)
	if newExpiry-date+1 < n.configs[obj.Cat].MinUnsealDays {
		return domain.NewError(domain.ErrConditionUnmet, "启封保障天数不足")
	}
	obj.Expiry = newExpiry
	obj.Status = domain.StatusInService
	obj.SealAnchor = 0
	n.accept(date)
	return nil
}

// Attach 挂接或转移。
func (n *Naive) Attach(deviceID, accID string, date int) error {
	if deviceID == "" || accID == "" || deviceID == accID || date < 0 {
		return domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	if err := n.clock(date); err != nil {
		return err
	}
	dev, err := n.get(deviceID)
	if err != nil {
		return err
	}
	acc, err := n.get(accID)
	if err != nil {
		return err
	}
	if !dev.Cat.IsDevice() || !acc.Cat.IsAccessory() {
		return domain.NewError(domain.ErrInvalidParam, "类别不匹配")
	}
	if acc.HostID == deviceID {
		return domain.NewError(domain.ErrStateNotAllowed, "附件已挂接在该设备上")
	}
	if acc.Status != domain.StatusInService {
		return domain.NewError(domain.ErrConditionUnmet, "附件封存或停用，不得挂接")
	}
	if overdue(acc.Expiry, date) {
		return domain.NewError(domain.ErrConditionUnmet, "附件已超期，不得挂接")
	}
	if acc.HostID != "" {
		delete(n.objects[acc.HostID].Attach, accID)
	}
	acc.HostID = deviceID
	dev.Attach[accID] = true
	n.accept(date)
	return nil
}

// Detach 摘除。
func (n *Naive) Detach(accID string, date int) error {
	if accID == "" || date < 0 {
		return domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	if err := n.clock(date); err != nil {
		return err
	}
	acc, err := n.get(accID)
	if err != nil {
		return err
	}
	if !acc.Cat.IsAccessory() {
		return domain.NewError(domain.ErrInvalidParam, "对象不是附件")
	}
	if acc.HostID == "" {
		return domain.NewError(domain.ErrStateNotAllowed, "附件未挂接")
	}
	delete(n.objects[acc.HostID].Attach, accID)
	acc.HostID = ""
	n.accept(date)
	return nil
}

// usable 判定设备可使用性，返回第一个不满足的原因。
func (n *Naive) usable(dev *Object, date int) (bool, *system.UseDenial) {
	if dev.Status == domain.StatusSealed {
		return false, &system.UseDenial{Reason: "sealed", Detail: "设备自身封存"}
	}
	if dev.Status == domain.StatusSuspended {
		return false, &system.UseDenial{Reason: "suspended", Detail: "设备自身停用"}
	}
	if overdue(dev.Expiry, date) {
		return false, &system.UseDenial{Reason: "overdue", Detail: "设备自身超期"}
	}
	accs := make([]string, 0, len(dev.Attach))
	for id := range dev.Attach {
		accs = append(accs, id)
	}
	sort.Strings(accs)
	hasValve := false
	for _, id := range accs {
		if n.objects[id].Cat == domain.CatSafetyValve {
			hasValve = true
		}
	}
	if !hasValve {
		return false, &system.UseDenial{Reason: "no_safety_valve", Detail: "缺少安全阀"}
	}
	for _, id := range accs {
		acc := n.objects[id]
		if acc.Status == domain.StatusSealed {
			return false, &system.UseDenial{Reason: "accessory", AccID: id, Detail: "附件封存: " + id}
		}
		if acc.Status == domain.StatusSuspended {
			return false, &system.UseDenial{Reason: "accessory", AccID: id, Detail: "附件停用: " + id}
		}
		if overdue(acc.Expiry, date) {
			return false, &system.UseDenial{Reason: "accessory", AccID: id, Detail: "附件超期: " + id}
		}
	}
	return true, nil
}

// RegisterUse 使用登记。
func (n *Naive) RegisterUse(deviceID string, date int) (bool, *system.UseDenial, error) {
	if deviceID == "" || date < 0 {
		return false, nil, domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	if err := n.clock(date); err != nil {
		return false, nil, err
	}
	dev, err := n.get(deviceID)
	if err != nil {
		return false, nil, err
	}
	if !dev.Cat.IsDevice() {
		return false, nil, domain.NewError(domain.ErrInvalidParam, "对象不是设备")
	}
	ok, denial := n.usable(dev, date)
	if !ok {
		return false, denial, nil
	}
	n.accept(date)
	return true, nil, nil
}

// Usable 可使用性查询（只读）。
func (n *Naive) Usable(deviceID string, date int) (bool, *system.UseDenial, error) {
	if deviceID == "" || date < 0 {
		return false, nil, domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	dev, err := n.get(deviceID)
	if err != nil {
		return false, nil, err
	}
	if !dev.Cat.IsDevice() {
		return false, nil, domain.NewError(domain.ErrInvalidParam, "对象不是设备")
	}
	ok, denial := n.usable(dev, date)
	return ok, denial, nil
}

// Warn 预警查询（全表扫描）。
func (n *Naive) Warn(date int) ([]system.WarnEntry, error) {
	if date < 0 {
		return nil, domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	self := map[string]int{}
	via := map[string][]string{}
	viaMin := map[string]int{}
	for _, obj := range n.objects {
		if obj.Status != domain.StatusInService {
			continue
		}
		if obj.Expiry < date || obj.Expiry > date+n.configs[obj.Cat].WarnAheadDays {
			continue
		}
		self[obj.ID] = obj.Expiry
		if obj.Cat.IsAccessory() && obj.HostID != "" {
			host := n.objects[obj.HostID]
			if host.Status == domain.StatusInService {
				via[host.ID] = append(via[host.ID], obj.ID)
				if m, ok := viaMin[host.ID]; !ok || obj.Expiry < m {
					viaMin[host.ID] = obj.Expiry
				}
			}
		}
	}
	type item struct {
		entry   system.WarnEntry
		sortKey int
	}
	items := []item{}
	seen := map[string]bool{}
	for id, expiry := range self {
		obj := n.objects[id]
		it := item{entry: system.WarnEntry{ID: id, Cat: obj.Cat, Expiry: expiry}, sortKey: expiry}
		if v, ok := via[id]; ok {
			sort.Strings(v)
			it.entry.Via = v
		}
		items = append(items, it)
		seen[id] = true
	}
	for id, v := range via {
		if seen[id] {
			continue
		}
		obj := n.objects[id]
		sort.Strings(v)
		items = append(items, item{
			entry:   system.WarnEntry{ID: id, Cat: obj.Cat, Expiry: obj.Expiry, Via: v},
			sortKey: viaMin[id],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].sortKey != items[j].sortKey {
			return items[i].sortKey < items[j].sortKey
		}
		return items[i].entry.ID < items[j].entry.ID
	})
	out := make([]system.WarnEntry, len(items))
	for i, it := range items {
		out[i] = it.entry
	}
	return out, nil
}

// Scrap 报废。
func (n *Naive) Scrap(id string, date int) error {
	if id == "" || date < 0 {
		return domain.NewError(domain.ErrInvalidParam, "参数非法")
	}
	if err := n.clock(date); err != nil {
		return err
	}
	obj, err := n.get(id)
	if err != nil {
		return err
	}
	if obj.Cat.IsDevice() {
		for accID := range obj.Attach {
			n.objects[accID].HostID = ""
		}
		obj.Attach = make(map[string]bool)
	} else if obj.HostID != "" {
		delete(n.objects[obj.HostID].Attach, obj.ID)
		obj.HostID = ""
	}
	obj.Status = domain.StatusScrapped
	obj.SealAnchor = 0
	n.accept(date)
	return nil
}
