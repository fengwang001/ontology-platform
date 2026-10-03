package ontology

// addByRefs 处理途径 (1)：把 id 与 L 全部节点并入同一线程。
func (m *Merger) addByRefs(id string, linkSet []string, subject, norm string, ts int64, idExisted bool) AddResult {
	// 先创建此前不存在的占位节点（只有占位的连通块不进 bySubject）。
	for _, r := range linkSet {
		if _, ok := m.parent[r]; !ok {
			m.parent[r] = r
			m.meta[r] = &comp{reals: make(map[string]struct{}), size: 1}
		}
	}
	if !idExisted {
		m.parent[id] = id
		m.meta[id] = &comp{reals: make(map[string]struct{}), size: 1}
	}

	// 操作前涉及的线程编号（id 原属线程 + L 各节点原属线程），去重。
	// 只有占位的连通块没有线程编号（root 为空），不计入 Gone。
	preSet := make(map[string]struct{})
	if r := m.meta[m.find(id)].root; r != "" {
		preSet[r] = struct{}{}
	}
	for _, r := range linkSet {
		if tr := m.meta[m.find(r)].root; tr != "" {
			preSet[tr] = struct{}{}
		}
	}

	target := m.find(id)
	for _, r := range linkSet {
		if rr := m.find(r); rr != target {
			target = m.union(target, rr)
		}
	}

	// 登记 id 为真实邮件（此前可能是占位）。
	if _, real := m.mail[id]; !real {
		m.mail[id] = realMail{ts: ts}
		m.subj[id] = subject
		c := m.meta[target]
		c.reals[id] = struct{}{}
		if c.root == "" {
			// 该连通块第一次拥有真实邮件：建立根与主题索引。
			c.root = id
			c.rootTS = ts
			c.minTS = ts
			c.maxTS = ts
			c.normSubj = norm
			m.indexAdd(norm, target)
		} else {
			m.addRealStats(c, id, ts, norm)
		}
	}

	threadID := m.meta[target].root
	return AddResult{ThreadID: threadID, Way: "ref", Gone: goneList(preSet, threadID)}
}

// addBySubject 处理途径 (2)：并入已选中的候选线程。
func (m *Merger) addBySubject(id, subject, norm string, ts int64, candDSU string) AddResult {
	// 途径 (2) 下 L 为空且 id 此前不存在。
	preThread := m.meta[candDSU].root
	m.parent[id] = candDSU
	c := m.meta[candDSU]
	c.size++

	m.mail[id] = realMail{ts: ts}
	m.subj[id] = subject
	c.reals[id] = struct{}{}
	m.addRealStats(c, id, ts, norm)

	return AddResult{
		ThreadID: c.root,
		Way:      "subject",
		Gone:     goneList(map[string]struct{}{preThread: {}}, c.root),
	}
}

// addNew 处理途径 (3)：新建只含该邮件的线程。
func (m *Merger) addNew(id, subject, norm string, ts int64) AddResult {
	c := &comp{
		reals:    map[string]struct{}{id: {}},
		root:     id,
		rootTS:   ts,
		minTS:    ts,
		maxTS:    ts,
		normSubj: norm,
		size:     1,
	}
	m.parent[id] = id
	m.meta[id] = c
	m.mail[id] = realMail{ts: ts}
	m.subj[id] = subject
	m.indexAdd(norm, id)
	return AddResult{ThreadID: id, Way: "new", Gone: []string{}}
}

// addRealStats 把一封真实邮件并入已有线程的统计，必要时更换线程根与主题索引。
// 调用前调用方已把 id 放入 c.reals。
func (m *Merger) addRealStats(c *comp, id string, ts int64, norm string) {
	if ts < c.minTS {
		c.minTS = ts
	}
	if ts > c.maxTS {
		c.maxTS = ts
	}
	// 线程根为 (ts, id) 最小的真实邮件；更早邮件到达时编号改变。
	if ts < c.rootTS || (ts == c.rootTS && id < c.root) {
		m.indexRemove(c.normSubj, m.find(c.root))
		c.root = id
		c.rootTS = ts
		c.normSubj = norm
		m.indexAdd(norm, m.find(id))
	}
}

// pickSubjectCandidate 在规范化主题相同的线程中选候选：
// minTS <= ts、ts-maxTS <= W（maxTS>ts 时差为负，满足）；
// 多个候选取 maxTS 最大者，并列取线程编号字节序小者；无候选返回 ""。
func (m *Merger) pickSubjectCandidate(norm string, ts int64) string {
	bestDSU := ""
	bestRoot := ""
	var bestMax int64
	for dsuRoot := range m.bySubject[norm] {
		m.subjectExamines++
		c := m.meta[dsuRoot]
		if c.minTS > ts {
			continue
		}
		if ts-c.maxTS > m.w {
			continue
		}
		if bestDSU == "" || c.maxTS > bestMax || (c.maxTS == bestMax && c.root < bestRoot) {
			bestDSU = dsuRoot
			bestRoot = c.root
			bestMax = c.maxTS
		}
	}
	return bestDSU
}

// union 合并两个 DSU 根（按小并大），迁移真实成员集合与主题索引，返回新根。
func (m *Merger) union(a, b string) string {
	ca, cb := m.meta[a], m.meta[b]
	small, big, cs, cb2 := a, b, ca, cb
	if ca.size > cb.size {
		small, big, cs, cb2 = b, a, cb, ca
	}
	m.parent[small] = big
	cb2.size += cs.size

	bigHas, smallHas := len(cb2.reals) > 0, len(cs.reals) > 0
	switch {
	case smallHas && !bigHas:
		// 大者原本全占位：小者根信息整体搬入，主题索引键从 small 改为 big。
		cb2.root = cs.root
		cb2.rootTS = cs.rootTS
		cb2.minTS = cs.minTS
		cb2.maxTS = cs.maxTS
		cb2.normSubj = cs.normSubj
		m.indexRemove(cs.normSubj, small)
		m.indexAdd(cs.normSubj, big)
		for r := range cs.reals {
			cb2.reals[r] = struct{}{}
		}
	case bigHas && smallHas:
		// 两边都是线程：真实成员集合按小并大逐元素搬移。
		for r := range cs.reals {
			cb2.reals[r] = struct{}{}
		}
		m.indexRemove(cs.normSubj, small)
		if cs.minTS < cb2.minTS {
			cb2.minTS = cs.minTS
		}
		if cs.maxTS > cb2.maxTS {
			cb2.maxTS = cs.maxTS
		}
		// 根取 (ts, id) 最小者；根若变化，主题索引随之改键。
		if cs.rootTS < cb2.rootTS || (cs.rootTS == cb2.rootTS && cs.root < cb2.root) {
			m.indexRemove(cb2.normSubj, big)
			cb2.root = cs.root
			cb2.rootTS = cs.rootTS
			cb2.normSubj = cs.normSubj
			m.indexAdd(cs.normSubj, big)
		}
	}
	cs.reals = nil
	return big
}

func (m *Merger) indexAdd(norm string, dsuRoot string) {
	set := m.bySubject[norm]
	if set == nil {
		set = make(map[string]struct{})
		m.bySubject[norm] = set
	}
	set[dsuRoot] = struct{}{}
}

func (m *Merger) indexRemove(norm string, dsuRoot string) {
	if set := m.bySubject[norm]; set != nil {
		delete(set, dsuRoot)
		if len(set) == 0 {
			delete(m.bySubject, norm)
		}
	}
}
