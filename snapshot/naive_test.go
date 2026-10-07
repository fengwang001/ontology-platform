package snapshot_test

import "ontology/snapshot"

// naiveValidate 是独立于生产实现的朴素逐条校验模型：
// 不共享生产代码的任何内部状态，仅依据规范文字重新实现一遍，
// 用于随机差分测试的结果对照。
func naiveValidate(records []snapshotRecord) naiveResult {
	type objInfo struct{ typ string }
	known := map[string]objInfo{}

	for i, r := range records {
		switch r.kind {
		case "object":
			if r.id == nil || r.typ == nil || *r.id == "" || *r.typ == "" {
				return naiveResult{truncated: true, prefix: i, bad: i, reason: "object_corrupt"}
			}
			if prev, ok := known[*r.id]; ok && prev.typ != *r.typ {
				return naiveResult{truncated: true, prefix: i, bad: i, reason: "object_corrupt"}
			}
			known[*r.id] = objInfo{*r.typ}
		case "link":
			badFields := r.src == nil || r.dst == nil || r.linkType == nil ||
				*r.src == "" || *r.dst == "" || *r.linkType == "" ||
				(r.dir != snapshot.DirForward && r.dir != snapshot.DirReverse)
			if badFields {
				return naiveResult{truncated: true, prefix: i, bad: i, reason: "link_corrupt"}
			}
			if _, ok := known[*r.src]; !ok {
				return naiveResult{truncated: true, prefix: i, bad: i, reason: "reference_missing"}
			}
			if _, ok := known[*r.dst]; !ok {
				return naiveResult{truncated: true, prefix: i, bad: i, reason: "reference_missing"}
			}
		default:
			// 无法证明是链接记录，按最高优先级类别（对象字段损坏）归类。
			return naiveResult{truncated: true, prefix: i, bad: i, reason: "object_corrupt"}
		}
	}
	return naiveResult{truncated: false, prefix: len(records), bad: -1, reason: "none"}
}

type snapshotRecord struct {
	kind               string
	id, typ            *string
	src, dst, linkType *string
	dir                snapshot.Direction
}

type naiveResult struct {
	truncated bool
	prefix    int
	bad       int
	reason    string
}
