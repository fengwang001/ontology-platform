package layercfg

// keyState 保存某一键在全部层上的写入与锁定（发布成功后内容不可变）。
type keyState struct {
	writes map[Ref]*Entry
	locks  map[Ref]bool
}

// snapshot 是版本化配置在某一版本的完整不可变状态。
// 未修改键的 keyState 与父版本共享同一指针，历史不复制整份配置。
type snapshot struct {
	version int
	keys    map[string]*keyState
}

func initialSnapshot() *snapshot {
	return &snapshot{version: 0, keys: map[string]*keyState{}}
}

// stateOf 取某键状态，缺失返回空状态（只读，禁止写入返回值）。
func stateOf(snap *snapshot, key string) *keyState {
	if snap != nil {
		if ks, ok := snap.keys[key]; ok {
			return ks
		}
	}
	return &keyState{}
}

// refValid 校验层与限定名是否匹配。
func refValid(ref Ref) error {
	switch ref.Layer {
	case LayerGlobal:
		if ref.Env != "" || ref.Region != "" || ref.Instance != "" {
			return errorf(ErrInvalidArgument, "global layer ref must carry no env/region/instance qualifiers")
		}
	case LayerEnv:
		if ref.Env == "" || ref.Region != "" || ref.Instance != "" {
			return errorf(ErrInvalidArgument, "env layer ref requires env and no region/instance qualifiers")
		}
	case LayerRegion:
		if ref.Env == "" || ref.Region == "" || ref.Instance != "" {
			return errorf(ErrInvalidArgument, "region layer ref requires env+region and no instance qualifier")
		}
	case LayerInstance:
		if ref.Env == "" || ref.Region == "" || ref.Instance == "" {
			return errorf(ErrInvalidArgument, "instance layer ref requires env+region+instance qualifiers")
		}
	default:
		return errorf(ErrInvalidArgument, "unknown layer %d", ref.Layer)
	}
	return nil
}

// scopeValid 校验解析三元组：窄限定必须逐级带齐父限定。
func scopeValid(scope Scope) error {
	if scope.Env == "" && (scope.Region != "" || scope.Instance != "") {
		return errorf(ErrInvalidArgument, "region/instance scope requires an env")
	}
	if scope.Region == "" && scope.Instance != "" {
		return errorf(ErrInvalidArgument, "instance scope requires a region")
	}
	return nil
}

// cloneForWrite 复制某键状态以便写时复制；空状态返回全新容器。
func cloneForWrite(ks *keyState) *keyState {
	cp := &keyState{
		writes: make(map[Ref]*Entry, len(ks.writes)),
		locks:  make(map[Ref]bool, len(ks.locks)),
	}
	for ref, e := range ks.writes {
		cp.writes[ref] = e
	}
	for ref := range ks.locks {
		cp.locks[ref] = true
	}
	return cp
}

func (ks *keyState) isEmpty() bool {
	return len(ks.writes) == 0 && len(ks.locks) == 0
}
