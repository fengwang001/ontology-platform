package restore

import "sort"

// classifyClasses 负责单类备份内部的可用范围判定。
// 它不感知跨类别依赖，只回答“这个类备份整体是否可用、内部哪些记录完好”。
func classifyClasses(snap *Snapshot) map[Class]ClassStatus {
	out := make(map[Class]ClassStatus, len(Classes()))
	for _, c := range Classes() {
		out[c] = classifyOne(snap, c)
	}
	return out
}

// classifyOne 判定单个类别的内部可用性。
//
// 规则（不做任何跨类推断）：
//   - 备份整体缺失或整体损坏 -> ClassUnavailable，即使切片里残留记录片段也忽略，
//     不允许凭残留数据反推缺失的类型定义；
//   - 备份存在：同时存在完好与损坏记录 -> ClassPartial；全部损坏 -> ClassUnavailable；
//     全部完好（含空备份）-> ClassOK。
func classifyOne(snap *Snapshot, c Class) ClassStatus {
	if classUnavailable(snap, c) {
		return ClassUnavailable
	}
	intact, corrupt := 0, 0
	for _, st := range classRecordStates(snap, c) {
		switch st {
		case StateIntact:
			intact++
		case StateCorrupt:
			corrupt++
		}
	}
	switch {
	case intact > 0 && corrupt > 0:
		return ClassPartial
	case corrupt > 0:
		return ClassUnavailable
	default:
		return ClassOK
	}
}

// classRecordStates 确定性返回某类备份内全部记录的原始状态。
func classRecordStates(snap *Snapshot, c Class) []RecordState {
	switch c {
	case ClassType:
		states := make([]RecordState, len(snap.Types))
		for i, r := range snap.Types {
			states[i] = r.State
		}
		return states
	case ClassObject:
		states := make([]RecordState, len(snap.Objects))
		for i, r := range snap.Objects {
			states[i] = r.State
		}
		return states
	case ClassLink:
		states := make([]RecordState, len(snap.Links))
		for i, r := range snap.Links {
			states[i] = r.State
		}
		return states
	case ClassAction:
		states := make([]RecordState, len(snap.Actions))
		for i, r := range snap.Actions {
			states[i] = r.State
		}
		return states
	default:
		return nil
	}
}

// recordState 返回某条记录在其所属备份内部的原始状态；
// 记录不存在时第二返回值为 false。
func recordState(snap *Snapshot, id RecordID) (RecordState, bool) {
	switch id.Class {
	case ClassType:
		for _, r := range snap.Types {
			if r.Key == id.Key {
				return r.State, true
			}
		}
	case ClassObject:
		for _, r := range snap.Objects {
			if r.Key == id.Key {
				return r.State, true
			}
		}
	case ClassLink:
		for _, r := range snap.Links {
			if r.Key == id.Key {
				return r.State, true
			}
		}
	case ClassAction:
		for _, r := range snap.Actions {
			if r.Key == id.Key {
				return r.State, true
			}
		}
	}
	return StateCorrupt, false
}

// classUnavailable 报告某类备份是否整体缺失或整体损坏。
func classUnavailable(snap *Snapshot, c Class) bool {
	cb, ok := snap.Classes[c]
	if !ok {
		// 未声明类别的备份视为正常存放（空备份也是合法输入）。
		return false
	}
	return cb.Missing || cb.CorruptAll
}

// allRecordIDs 按 (类别序, 键序) 确定性列出快照中出现过的全部记录。
func allRecordIDs(snap *Snapshot) []RecordID {
	ids := make([]RecordID, 0, len(snap.Types)+len(snap.Objects)+len(snap.Links)+len(snap.Actions))
	for _, c := range Classes() {
		keys := make([]string, 0)
		seen := map[string]bool{}
		for _, id := range rawKeys(snap, c) {
			if !seen[id] {
				seen[id] = true
				keys = append(keys, id)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			ids = append(ids, RecordID{Class: c, Key: k})
		}
	}
	return ids
}

// rawKeys 收集某类备份切片中出现的全部键（未排序，含重复）。
func rawKeys(snap *Snapshot, c Class) []string {
	switch c {
	case ClassType:
		keys := make([]string, len(snap.Types))
		for i, r := range snap.Types {
			keys[i] = r.Key
		}
		return keys
	case ClassObject:
		keys := make([]string, len(snap.Objects))
		for i, r := range snap.Objects {
			keys[i] = r.Key
		}
		return keys
	case ClassLink:
		keys := make([]string, len(snap.Links))
		for i, r := range snap.Links {
			keys[i] = r.Key
		}
		return keys
	case ClassAction:
		keys := make([]string, len(snap.Actions))
		for i, r := range snap.Actions {
			keys[i] = r.Key
		}
		return keys
	}
	return nil
}
