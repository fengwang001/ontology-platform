package sorting

// 朴素模型：独立编写的参照实现。
// 刻意使用最直观的数据结构（切片 + 全量线性扫描），
// 不做任何索引与聚合缓存，以“显然正确”换取“显然缓慢”。
// 随机对照测试用它验证 Hub 的优化实现行为完全一致。

import "sort"

type naiveBag struct {
	id           int64
	site         string
	status       BagStatus
	parcels      []Parcel
	firstAddTime int64
	sealTime     int64
	departTime   int64
	trainNo      string
	unpackTime   int64
	unpackSite   string
	missing      []string
	extra        []string
}

type naiveHub struct {
	cfg    Config
	last   int64
	nextID int64
	bags   []*naiveBag
}

func newNaiveHub(cfg Config) *naiveHub {
	return &naiveHub{cfg: cfg, nextID: 1}
}

func (n *naiveHub) findOpen(site string) *naiveBag {
	for _, b := range n.bags {
		if b.site == site && b.status == BagOpen {
			return b
		}
	}
	return nil
}

func (n *naiveHub) findBag(id int64) *naiveBag {
	for _, b := range n.bags {
		if b.id == id {
			return b
		}
	}
	return nil
}

// inField 通过全量扫描判断运单号是否在场：
// 在袋内、且（袋未拆袋，或拆袋时缺失未扫到）。
func (n *naiveHub) inField(waybill string) bool {
	for _, b := range n.bags {
		for _, p := range b.parcels {
			if p.Waybill != waybill {
				continue
			}
			if b.status != BagUnpacked {
				return true
			}
			for _, m := range b.missing {
				if m == waybill {
					return true
				}
			}
		}
	}
	return false
}

func (n *naiveHub) checkClock(now int64) error {
	if now < n.last {
		return errClockRollback(now, n.last)
	}
	return nil
}

func (n *naiveHub) add(p Parcel, now int64) (int64, error) {
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
	if err := n.checkClock(now); err != nil {
		return 0, err
	}
	if n.inField(p.Waybill) {
		return 0, errBusinessReject("运单号 %s 已在场内，重复加入", p.Waybill)
	}
	if p.Weight > n.cfg.MaxBagWeight {
		return 0, errBusinessReject("单件重量 %d 超过集袋总重上限 %d，无法分拣", p.Weight, n.cfg.MaxBagWeight)
	}

	n.last = now
	b := n.findOpen(p.Site)
	if b != nil {
		var total int64
		hasFragile, hasLiquid := false, false
		for _, q := range b.parcels {
			total += q.Weight
			if q.Category == CategoryFragile {
				hasFragile = true
			}
			if q.Category == CategoryLiquid {
				hasLiquid = true
			}
		}
		full := len(b.parcels)+1 > n.cfg.MaxBagCount ||
			total+p.Weight > n.cfg.MaxBagWeight ||
			(p.Category == CategoryFragile && hasLiquid) ||
			(p.Category == CategoryLiquid && hasFragile) ||
			now-b.firstAddTime >= n.cfg.DwellLimit
		if full {
			b.status = BagSealed
			b.sealTime = now
			b = nil
		}
	}
	if b == nil {
		b = &naiveBag{
			id:           n.nextID,
			site:         p.Site,
			status:       BagOpen,
			firstAddTime: now,
			sealTime:     -1,
			departTime:   -1,
			unpackTime:   -1,
		}
		n.nextID++
		n.bags = append(n.bags, b)
	}
	b.parcels = append(b.parcels, p)
	return b.id, nil
}

func (n *naiveHub) seal(site string, now int64) (int64, error) {
	if site == "" {
		return 0, errInvalidParam("目的网点编码为空")
	}
	if now < 0 {
		return 0, errInvalidParam("时刻必须为非负整数，实际 %d", now)
	}
	if err := n.checkClock(now); err != nil {
		return 0, err
	}
	b := n.findOpen(site)
	if b == nil {
		return 0, errNotFound("网点 %s 无开放集袋", site)
	}
	n.last = now
	b.status = BagSealed
	b.sealTime = now
	return b.id, nil
}

func (n *naiveHub) depart(bagID int64, trainNo string, now int64) error {
	if bagID < 1 {
		return errInvalidParam("集袋编号必须为正整数，实际 %d", bagID)
	}
	if trainNo == "" {
		return errInvalidParam("车次为空")
	}
	if now < 0 {
		return errInvalidParam("时刻必须为非负整数，实际 %d", now)
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	b := n.findBag(bagID)
	if b == nil {
		return errNotFound("集袋 %d 不存在", bagID)
	}
	if b.status != BagSealed {
		return errStateMismatch("集袋 %d 状态为 %s，仅已封存的集袋可出场", bagID, b.status)
	}
	n.last = now
	b.status = BagDeparted
	b.departTime = now
	b.trainNo = trainNo
	return nil
}

func (n *naiveHub) unpack(bagID int64, site string, scanned []string, now int64) (UnpackResult, error) {
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
	if err := n.checkClock(now); err != nil {
		return UnpackResult{}, err
	}
	b := n.findBag(bagID)
	if b == nil {
		return UnpackResult{}, errNotFound("集袋 %d 不存在", bagID)
	}
	if b.status != BagDeparted {
		return UnpackResult{}, errStateMismatch("集袋 %d 状态为 %s，仅已出场的集袋可拆袋", bagID, b.status)
	}
	if b.site != site {
		return UnpackResult{}, errSiteMismatch("集袋 %d 目的网点为 %s，与给定网点 %s 不一致", bagID, b.site, site)
	}

	n.last = now
	var missing, extra []string
	for _, p := range b.parcels {
		if !seen[p.Waybill] {
			missing = append(missing, p.Waybill)
		}
	}
	inBag := make(map[string]bool, len(b.parcels))
	for _, p := range b.parcels {
		inBag[p.Waybill] = true
	}
	for w := range seen {
		if !inBag[w] {
			extra = append(extra, w)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	b.status = BagUnpacked
	b.unpackTime = now
	b.unpackSite = site
	b.missing = missing
	b.extra = extra
	return UnpackResult{BagID: bagID, Site: site, Missing: missing, Extra: extra}, nil
}

// snapshot 输出与 Hub.BagInfo 对齐的全量快照，供对照测试逐袋比较。
func (n *naiveHub) snapshot() []BagInfo {
	infos := make([]BagInfo, 0, len(n.bags))
	for _, b := range n.bags {
		info := BagInfo{
			ID:            b.id,
			Site:          b.site,
			Status:        b.status,
			TotalWeight:   0,
			FirstAddTime:  b.firstAddTime,
			SealTime:      b.sealTime,
			DepartTime:    b.departTime,
			TrainNo:       b.trainNo,
			UnpackTime:    b.unpackTime,
			UnpackSite:    b.unpackSite,
			UnpackMissing: append([]string(nil), b.missing...),
			UnpackExtra:   append([]string(nil), b.extra...),
		}
		for _, p := range b.parcels {
			info.Waybills = append(info.Waybills, p.Waybill)
			info.TotalWeight += p.Weight
		}
		infos = append(infos, info)
	}
	return infos
}
