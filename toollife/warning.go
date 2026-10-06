package toollife

// remaining 返回剩余寿命（不为负）。
func remaining(t *Tool, limit uint64) uint64 {
	if t.used >= limit {
		return 0
	}
	return limit - t.used
}

// usedPermille 返回已用占寿命上限的千分比（向下取整）。
func usedPermille(used, limit uint64) uint64 {
	if limit == 0 {
		// 寿命上限为 0 时，任何正已用都视为 1000‰；已用为 0 视为 0‰。
		if used == 0 {
			return 0
		}
		return 1000
	}
	return used * 1000 / limit
}
