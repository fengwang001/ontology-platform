package pivas

// UpsertDrug 登记或变更药品目录条目，仅对之后受理的医嘱生效。
func (c *Center) UpsertDrug(now int64, d Drug) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d.ID == "" || d.SolventClass == "" {
		return errf(CodeInvalidParam, "药品标识与溶媒类别必须非空: %+v", d)
	}
	if d.RoomStableSec <= 0 || d.ColdStableSec <= 0 {
		return errf(CodeInvalidParam, "稳定秒数必须为正整数: %+v", d)
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	c.drugs[d.ID] = d
	c.accept(now)
	return nil
}

// AddIncompatibility 登记一对无序禁忌配对。
func (c *Center) AddIncompatibility(now int64, a, b string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a == "" || b == "" || a == b {
		return errf(CodeInvalidParam, "禁忌配对须为两个不同的非空标识: %q %q", a, b)
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	c.pairs[canonPair(a, b)] = struct{}{}
	c.accept(now)
	return nil
}

// RemoveIncompatibility 移除一对禁忌配对；不存在的配对视为无操作。
func (c *Center) RemoveIncompatibility(now int64, a, b string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a == "" || b == "" || a == b {
		return errf(CodeInvalidParam, "禁忌配对须为两个不同的非空标识: %q %q", a, b)
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	delete(c.pairs, canonPair(a, b))
	c.accept(now)
	return nil
}

// RegisterBench 登记洁净台；编号不得重复。
func (c *Center) RegisterBench(now int64, cfg BenchConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateBenchConfig(cfg); err != nil {
		return err
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	if _, dup := c.benches[cfg.ID]; dup {
		return errf(CodeInvalidParam, "洁净台编号重复: %q", cfg.ID)
	}
	c.benches[cfg.ID] = &bench{cfg: cfg}
	c.benchOrder = insertSorted(c.benchOrder, cfg.ID)
	c.accept(now)
	return nil
}

func validateBenchConfig(cfg BenchConfig) error {
	if cfg.ID == "" {
		return errf(CodeInvalidParam, "洁净台编号必须非空")
	}
	if cfg.Capacity <= 0 {
		return errf(CodeInvalidParam, "批容量必须为正整数: %d", cfg.Capacity)
	}
	if cfg.ClearanceSec <= 0 {
		return errf(CodeInvalidParam, "清场间隔必须为正整数: %d", cfg.ClearanceSec)
	}
	if len(cfg.DurationByCount) != cfg.Capacity+1 {
		return errf(CodeInvalidParam, "配置时长对照须覆盖数量 1..%d", cfg.Capacity)
	}
	prev := int64(0)
	for n := 1; n <= cfg.Capacity; n++ {
		d := cfg.DurationByCount[n]
		if d <= 0 {
			return errf(CodeInvalidParam, "配置时长必须为正整数: 数量 %d 时长 %d", n, d)
		}
		if d <= prev {
			return errf(CodeInvalidParam, "配置时长须随数量严格递增: 数量 %d 时长 %d", n, d)
		}
		prev = d
	}
	return nil
}

func canonPair(a, b string) [2]string {
	if a < b {
		return [2]string{a, b}
	}
	return [2]string{b, a}
}

func insertSorted(xs []string, x string) []string {
	i := 0
	for i < len(xs) && xs[i] < x {
		i++
	}
	xs = append(xs, "")
	copy(xs[i+1:], xs[i:])
	xs[i] = x
	return xs
}
