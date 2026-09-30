package anomaly

// model 是校验通过后的规范化内部模型。它只在单次调用内创建与使用，
// 因此 Analyze 天然可被并发调用。
type model struct {
	byID      map[int]*Txn
	committed map[int]bool

	// writers[k][(txn,seq)] = txn：键 k 上版本 (t,n) 的写事务 t。
	writers map[string]map[[2]int]int
	// lastWrite[k][t] = n：已提交事务 t 对键 k 的最后一次写序号。
	lastWrite map[string]map[int]int
	// writeSeq[k][t] 累计该事务对 k 的写次数，用于按操作次序给版本编号。
	writeSeq map[string]map[int]int

	// order[k] 是规范化后的版本次序（含 (0,0)）。
	order map[string][][2]int

	reads []readInfo
}

type readInfo struct {
	reader  int
	key     string
	version [2]int
}

// validate 按题面规定的顺序检查，只返回第一个命中的拒绝原因。
func validate(h History) (*model, string) {
	// 1. 编号为零或重复（编号必须为正）。
	seen := make(map[int]bool)
	for i := range h.Txns {
		id := h.Txns[i].ID
		if id <= 0 || seen[id] {
			return nil, RejectZeroID
		}
		seen[id] = true
	}

	m := &model{
		byID:      make(map[int]*Txn),
		committed: make(map[int]bool),
		writers:   make(map[string]map[[2]int]int),
		lastWrite: make(map[string]map[int]int),
		writeSeq:  make(map[string]map[int]int),
		order:     make(map[string][][2]int),
	}
	for i := range h.Txns {
		t := &h.Txns[i]
		m.byID[t.ID] = t
		m.committed[t.ID] = t.Status == Committed
	}

	// 预扫描所有写操作，给版本编号并登记写者；
	// 同时登记所有读操作，随后统一检查读引用。
	for i := range h.Txns {
		t := &h.Txns[i]
		for j := range t.Ops {
			op := &t.Ops[j]
			switch op.Type {
			case "write":
				seq := m.writeSeq[op.Key]
				if seq == nil {
					seq = make(map[int]int)
					m.writeSeq[op.Key] = seq
				}
				seq[t.ID]++
				n := seq[t.ID]
				if m.writers[op.Key] == nil {
					m.writers[op.Key] = make(map[[2]int]int)
				}
				m.writers[op.Key][[2]int{t.ID, n}] = t.ID
				if t.Status == Committed {
					if m.lastWrite[op.Key] == nil {
						m.lastWrite[op.Key] = make(map[int]int)
					}
					m.lastWrite[op.Key][t.ID] = n
				}
			case "read":
				var ver [2]int
				if op.ReadVersion != nil {
					ver = *op.ReadVersion
				}
				m.reads = append(m.reads, readInfo{
					reader:  t.ID,
					key:     op.Key,
					version: ver,
				})
			}
		}
	}

	// 2. 读引用不存在的版本。初始版本 (0,0) 总是存在。
	for _, r := range m.reads {
		if r.version == [2]int{0, 0} {
			continue
		}
		if _, ok := m.writers[r.key][r.version]; !ok {
			return nil, RejectBadReadVersion
		}
	}

	// 3. 版本次序校验：初始版本最先；其后恰好是每个已提交事务
	// 对该键的最后一次写，各出现一次。
	keys := make(map[string]bool)
	for k := range m.writers {
		keys[k] = true
	}
	for _, r := range m.reads {
		keys[r.key] = true
	}
	for k := range keys {
		given := h.Order[k]
		if len(given) == 0 || given[0] != [2]int{0, 0} {
			return nil, RejectBadOrder
		}
		rest := given[1:]

		want := make(map[[2]int]bool)
		lasts, _ := m.lastWrite[k]
		for t, n := range lasts {
			want[[2]int{t, n}] = true
		}
		if len(rest) != len(want) {
			return nil, RejectBadOrder
		}
		dup := make(map[[2]int]bool)
		for _, v := range rest {
			if v[0] <= 0 || dup[v] || !want[v] {
				return nil, RejectBadOrder
			}
		}
		m.order[k] = append(m.order[k], given...)
	}

	// 4. 事务数超过 12。
	if len(h.Txns) > 12 {
		return nil, RejectTooManyTxns
	}

	return m, ""
}
