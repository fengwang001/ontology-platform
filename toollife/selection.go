package toollife

// chooseTool 在刀组内按顺序选出第一把可用且能承载本次预计消耗的刀。
// 返回 ErrNoTool 或 ErrNoCapacity；不修改任何状态。
func chooseTool(g *Group, estimated uint64) (*Tool, error) {
	// 第一遍：按固定顺序找第一把能立即承载的可用刀。
	for _, t := range g.order {
		if t.status == StatusAvailable && canCarry(t, g.cfg, estimated) {
			return t, nil
		}
	}
	if len(g.order) == 0 {
		return nil, errf(ErrNoTool, "group %q has no tools", g.id)
	}
	// 第二遍：区分失败原因。只有当「所有刀都仅因现存预占而无法承载」
	// （即把预占全部释放后该刀即可承载）时才报暂无余量；
	// 任何破损/锁定/耗尽/自身余量不足都报无刀可用。
	for _, t := range g.order {
		if !reserveBlockedOnly(t, g.cfg, estimated) {
			return nil, errf(ErrNoTool, "no tool available in group %q", g.id)
		}
	}
	return nil, errf(ErrNoCapacity, "no free capacity in group %q (all tools fully reserved)", g.id)
}

// canCarry 判断刀在给定模式下能否承载本次预计消耗。
func canCarry(t *Tool, cfg GroupConfig, estimated uint64) bool {
	committed := t.used + t.reserved
	if cfg.Mode == ModeLenient {
		// 宽松：申请时已用与已预占之和尚未达到上限即可，允许本次使用超出上限。
		return committed < cfg.LifeLimit
	}
	// 严格：预占后（已用+已预占+本次预计）不得超过寿命上限。
	if committed > cfg.LifeLimit || estimated > cfg.LifeLimit-committed {
		return false
	}
	return true
}

// reserveBlockedOnly 判断一把刀「当前不能承载，但仅因现存预占而不能」：
// 刀必须可用，且释放掉全部预占后即可承载。返回 false 的情形包括
// 破损/锁定/耗尽、寿命上限为 0、严格模式下自身余量已不足本次申请等。
func reserveBlockedOnly(t *Tool, cfg GroupConfig, estimated uint64) bool {
	if t.status != StatusAvailable || t.reserved == 0 {
		return false
	}
	// 以预占清零后的状态判定：宽松即 used < limit；严格即 used+est <= limit。
	cleared := *t
	cleared.reserved = 0
	return canCarry(&cleared, cfg, estimated)
}
