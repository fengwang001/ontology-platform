package layercfg

// layerRefs 返回给定解析三元组沿从宽到窄链适用的层引用（至多 4 个）。
func layerRefs(scope Scope) []Ref {
	refs := []Ref{{Layer: LayerGlobal}}
	if scope.Env != "" {
		refs = append(refs, Ref{Layer: LayerEnv, Env: scope.Env})
	}
	if scope.Region != "" {
		refs = append(refs, Ref{Layer: LayerRegion, Env: scope.Env, Region: scope.Region})
	}
	if scope.Instance != "" {
		refs = append(refs, Ref{Layer: LayerInstance, Env: scope.Env, Region: scope.Region, Instance: scope.Instance})
	}
	return refs
}

// resolveKey 在某一不可变快照上解析单键。
// resolveKey 在某一不可变快照上解析单键。
// 沿从宽到窄链：普通覆盖值整体替换当前累积；取消标记清空累积（含追加列表）；
// 追加列表从宽到窄拼接，重复元素保留首次出现位置，之后重复忽略。
func resolveKey(snap *snapshot, schema Schema, scope Scope) Result {
	ks := stateOf(snap, schema.Key)
	present := false
	var acc Value
	seen := map[string]bool{}
	for _, ref := range layerRefs(scope) {
		e, ok := ks.writes[ref]
		if !ok {
			continue
		}
		if e.Kind == WriteCancel {
			present = false
			acc = Value{}
			seen = map[string]bool{}
			continue
		}
		if schema.Merge == MergeAppend {
			if !present || acc.Type == 0 {
				acc = Value{Type: TypeStringList}
			}
			for _, item := range e.Value.List {
				if !seen[item] {
					seen[item] = true
					acc.List = append(acc.List, item)
				}
			}
		} else {
			acc = cloneValue(e.Value)
		}
		present = true
	}
	if !present {
		return Result{Present: false}
	}
	return Result{Present: true, Value: acc}
}

// entityScopes 枚举快照中所有已存在的实体解析三元组
// （实体存在性以任一层的值槽写入中出现过其限定名为准，锁定不计）。
// entityScopes 枚举快照中所有已存在实体的解析三元组。
// 实体存在性以任一层的值槽写入中出现过其限定名为准，锁定不计。
// 含全局视图（Scope{}）：全局必填在此检查。
func entityScopes(snap *snapshot) []Scope {
	envs := map[string]bool{}
	regions := map[Scope]bool{}
	instances := map[Scope]bool{}
	for _, ks := range snap.keys {
		for ref := range ks.writes {
			switch ref.Layer {
			case LayerEnv:
				envs[ref.Env] = true
			case LayerRegion:
				envs[ref.Env] = true
				regions[Scope{Env: ref.Env, Region: ref.Region}] = true
			case LayerInstance:
				envs[ref.Env] = true
				regions[Scope{Env: ref.Env, Region: ref.Region}] = true
				instances[Scope{Env: ref.Env, Region: ref.Region, Instance: ref.Instance}] = true
			}
		}
	}
	scopes := make([]Scope, 0, 1+len(envs)+len(regions)+len(instances))
	scopes = append(scopes, Scope{})
	for env := range envs {
		scopes = append(scopes, Scope{Env: env})
	}
	for sc := range regions {
		scopes = append(scopes, sc)
	}
	for sc := range instances {
		scopes = append(scopes, sc)
	}
	return scopes
}
