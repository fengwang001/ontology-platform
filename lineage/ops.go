package lineage

import (
	"slices"
)

// PutSource 登记（或更新）一个源对象，返回其当前版本引用。
// 内容不变时版本保持不变；内容变化产生新版本，旧版本随之失效。
func (t *Tracker) PutSource(id, payload string) (Ref, error) {
	version := sourceVersion(payload)

	t.mu.Lock()
	defer t.mu.Unlock()

	if kind, exists := t.kinds[id]; exists && kind != Source {
		err := reject(ErrKindConflict, "put_source", &Ref{ID: id, Version: version},
			"该 ID 已登记为派生对象，不能复用为源对象")
		t.log.Error("源对象登记被拒绝", "原因", err.Error())
		return Ref{}, err
	}

	ref := Ref{ID: id, Version: version}
	if _, exists := t.nodes[id][version]; !exists {
		t.addNodeLocked(&Node{
			ID:        id,
			Version:   version,
			Kind:      Source,
			Operation: "source",
			CreatedAt: t.now(),
		})
	}

	active := true
	if cur, ok := t.currentVersionLocked(id); ok && cur != version {
		active = false
	}
	t.log.Info("源对象登记", "对象", ref.ID, "版本", ref.Version,
		"判定依据", "内容指纹；相同内容版本不变，内容变化旧版本失效",
		"当前版本", active)
	return ref, nil
}

// Derive 执行一次派生操作，并在操作时记录“每个输入版本 → 输出版本”的血缘。
// 任何校验失败都在状态变更前返回，被拒绝的操作不会改变血缘。
func (t *Tracker) Derive(req DeriveRequest) (Ref, error) {
	inputs := uniqueRefs(req.Inputs)

	t.mu.Lock()
	defer t.mu.Unlock()

	// 规则 1：派生必须在操作时记录至少一条输入血缘。
	if len(inputs) == 0 {
		err := reject(ErrNoLineage, req.Operation, &Ref{ID: req.OutputID},
			"派生操作 inputs 为空，未记录任何输入→输出血缘")
		t.log.Error("派生被拒绝", "原因", err.Error())
		return Ref{}, err
	}

	// 规则 2：血缘必须引用真实存在的版本，且必须是当前版本（不能指向过期版本）。
	for _, in := range inputs {
		node, exists := t.nodes[in.ID][in.Version]
		if !exists {
			err := reject(ErrRefNotFound, req.Operation, &in,
				"血缘记录的输入版本在对象版本表中不存在")
			t.log.Error("派生被拒绝", "输出", req.OutputID, "原因", err.Error())
			return Ref{}, err
		}
		cur, _ := t.currentVersionLocked(in.ID)
		if node.Version != cur {
			err := reject(ErrStaleVersion, req.Operation, &in,
				"输入版本 "+in.Version+" 已被当前版本 "+cur+" 取代，血缘不得指向过期版本")
			t.log.Error("派生被拒绝", "输出", req.OutputID, "原因", err.Error())
			return Ref{}, err
		}
	}

	// 规则 3：产生方式不可混用。
	if kind, exists := t.kinds[req.OutputID]; exists && kind != Derived {
		err := reject(ErrKindConflict, req.Operation, &Ref{ID: req.OutputID},
			"该 ID 已登记为源对象，不能再由派生产生")
		t.log.Error("派生被拒绝", "原因", err.Error())
		return Ref{}, err
	}

	version := derivedVersion(req.Operation, inputs, req.Payload)
	ref := Ref{ID: req.OutputID, Version: version}

	// 幂等：相同操作、相同输入版本、相同内容重复（含并发）提交，
	// 产生相同版本与相同血缘边，血缘图不发生改变。
	_, already := t.nodes[req.OutputID][version]
	if !already {
		nodeInputs := slices.Clone(inputs)
		t.addNodeLocked(&Node{
			ID:        req.OutputID,
			Version:   version,
			Kind:      Derived,
			Operation: req.Operation,
			Inputs:    nodeInputs,
			CreatedAt: t.now(),
		})
		for _, in := range inputs {
			t.addEdgeLocked(in, ref, req.Operation)
		}
	}

	cur, _ := t.currentVersionLocked(req.OutputID)
	t.log.Info("派生血缘记录",
		"操作", req.Operation, "输出", ref.ID, "版本", ref.Version,
		"输入数", len(inputs), "判定依据",
		"版本=sha256(操作,排序后输入版本,内容)；逐输入记录输入→输出边；校验全部通过后才落盘",
		"当前版本", cur == version, "幂等命中", already)
	return ref, nil
}

// Current 返回对象当前版本的引用。
func (t *Tracker) Current(id string) (Ref, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	v, ok := t.currentVersionLocked(id)
	if !ok {
		return Ref{}, false
	}
	return Ref{ID: id, Version: v}, true
}
