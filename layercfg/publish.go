package layercfg

// validatePublish 按错误优先级依次校验一批变更，返回校验通过后的候选快照。
// 任何阶段失败都不得改动入参快照。
func validatePublish(base *snapshot, schemas map[string]Schema, changes []Change) (*snapshot, error) {
	// 阶段 1：参数非法（操作种类、键名、层限定、各操作自身的取值要求）。
	for i, ch := range changes {
		if ch.Key == "" {
			return nil, errorf(ErrInvalidArgument, "change %d: key must not be empty", i)
		}
		if err := refValid(ch.Ref); err != nil {
			return nil, err
		}
		switch ch.Op {
		case OpSetValue:
			if ch.Value.Type == 0 {
				return nil, errorf(ErrInvalidArgument, "change %d: SetValue requires a typed value", i)
			}
		case OpCancel, OpClearWrite, OpLock, OpUnlock:
			if ch.Value.Type != 0 {
				return nil, errorf(ErrInvalidArgument, "change %d: op %d must not carry a value", i, ch.Op)
			}
		default:
			return nil, errorf(ErrInvalidArgument, "change %d: unknown change op %d", i, ch.Op)
		}
	}

	// 阶段 2：键未登记。
	for _, ch := range changes {
		if _, ok := schemas[ch.Key]; !ok {
			return nil, errorf(ErrKeyNotRegistered, "key %q is not registered", ch.Key)
		}
	}

	// 阶段 3：类型或范围非法。
	for _, ch := range changes {
		if ch.Op == OpSetValue {
			if err := checkValue(schemas[ch.Key], ch.Value); err != nil {
				return nil, err
			}
		}
	}

	// 阶段 4 前置：同发布内冲突。
	// 值槽（写值/取消/清除）与锁槽（锁定/解锁）是两个独立槽位；
	// 同一发布对同一层同一键的同一槽位出现两条及以上变更即非法。
	type slot struct {
		key string
		ref Ref
	}
	writeSeen := map[slot]bool{}
	lockSeen := map[slot]bool{}
	for _, ch := range changes {
		s := slot{key: ch.Key, ref: ch.Ref}
		switch ch.Op {
		case OpSetValue, OpCancel, OpClearWrite:
			if writeSeen[s] {
				return nil, errorf(ErrConflict, "conflicting changes for key %q at layer %s within one publish", ch.Key, ch.Ref.Layer)
			}
			writeSeen[s] = true
		case OpLock, OpUnlock:
			if lockSeen[s] {
				return nil, errorf(ErrConflict, "conflicting lock changes for key %q at layer %s within one publish", ch.Key, ch.Ref.Layer)
			}
			lockSeen[s] = true
		}
	}

	// 构造候选快照（写时复制：仅复制受影响键的 keyState）。
	cand := applyChanges(base, changes)

	// 阶段 4：锁定冲突。在候选快照上，锁定所在链的任何更窄层都不得有写入；
	// 同时覆盖“发布前已存在的窄层写入”与“同发布新写入/新增锁定”两类情形。
	for key, ks := range cand.keys {
		for lockRef := range ks.locks {
			for writeRef := range ks.writes {
				if narrowerInChain(lockRef, writeRef) {
					return nil, errorf(ErrLockConflict,
						"key %q is locked at layer %s but has a write at narrower layer %s",
						key, lockRef.Layer, writeRef.Layer)
				}
			}
		}
	}

	// 阶段 5：必填传播。发布后每个已存在实体的解析视图中，必填键均须有值。
	for _, scope := range entityScopes(cand) {
		for key, schema := range schemas {
			if !schema.Required {
				continue
			}
			if !resolveKey(cand, schema, scope).Present {
				return nil, errorf(ErrRequiredMissing,
					"required key %q is unset in scope env=%q region=%q instance=%q",
					key, scope.Env, scope.Region, scope.Instance)
			}
		}
	}
	return cand, nil
}

// narrowerInChain 判定 narrower 是否位于 anchor 锁定所作用的更窄链上：
// 层级严格更窄，且 narrower 在 anchor 层级及以上的限定名与 anchor 完全一致。
// env=prod 的锁定不约束 env=dev；region=prod/cn 的锁定只约束 prod/cn 下实例。
func narrowerInChain(anchor, narrower Ref) bool {
	if narrower.Layer <= anchor.Layer {
		return false
	}
	if anchor.Layer == LayerGlobal {
		return true
	}
	if narrower.Env != anchor.Env {
		return false
	}
	if anchor.Layer == LayerEnv {
		return true
	}
	if narrower.Region != anchor.Region {
		return false
	}
	if anchor.Layer == LayerRegion {
		return true
	}
	return false
}

// applyChanges 把变更写入候选快照；仅复制受影响键的 keyState，未变键共享父版本指针。
func applyChanges(base *snapshot, changes []Change) *snapshot {
	cand := &snapshot{
		version: base.version + 1,
		keys:    make(map[string]*keyState, len(base.keys)),
	}
	for key, ks := range base.keys {
		cand.keys[key] = ks
	}
	cloned := map[string]bool{}
	for _, ch := range changes {
		ks := cand.keys[ch.Key]
		if !cloned[ch.Key] {
			ks = cloneForWrite(stateOf(base, ch.Key))
			cand.keys[ch.Key] = ks
			cloned[ch.Key] = true
		}
		switch ch.Op {
		case OpSetValue:
			ks.writes[ch.Ref] = &Entry{Kind: WriteValue, Value: cloneValue(ch.Value)}
		case OpCancel:
			ks.writes[ch.Ref] = &Entry{Kind: WriteCancel}
		case OpClearWrite:
			delete(ks.writes, ch.Ref)
		case OpLock:
			ks.locks[ch.Ref] = true
		case OpUnlock:
			delete(ks.locks, ch.Ref)
		}
	}
	// 统一在所有变更应用后清理变空的键，避免中途删除导致同键后续变更重新从
	// 父版本克隆而回滚已应用的修改。
	for key := range cloned {
		if cand.keys[key].isEmpty() {
			delete(cand.keys, key)
		}
	}
	return cand
}
