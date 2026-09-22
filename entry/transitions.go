package entry

import "ontology/version"

// ApplyInvalidate 在回源之外应用一条失效通知（版本 ver）。
// 返回 true 表示该通知真正作废了一份有效数据（计入失效次数）；
// 返回 false 表示通知被丢弃：旧版本、水位重复，或没有可作废的数据。
// 该函数绝不触碰 Fetching 中的条目：那种情况由回源结束路径统一裁决。
func (e *Entry) ApplyInvalidate(ver version.Version) bool {
	if e.state == Fetching {
		return false
	}
	if !ver.Newer(e.seenVer) {
		return false // 旧通知或重复水位，幂等丢弃
	}
	e.seenVer = ver
	if e.state == Valid {
		e.state = Stale
		e.invalid++
		return true
	}
	return false // Hole/Stale：没有有效数据被作废
}

// NoteFetchingInvalidate 在回源进行中记录一条更高水位的通知。
// 返回 true 表示它使本次回源注定作废；同一回源内仅第一次生效。
func (e *Entry) NoteFetchingInvalidate(ver version.Version) bool {
	if e.state != Fetching || !ver.Newer(e.seenVer) {
		return false
	}
	e.seenVer = ver
	e.staleAt++
	return true
}

// CommitFetch 回源成功结束。
// p.Ver >= seenVer：结果新鲜，提交为 Valid（含负缓存），返回 true。
// p.Ver <  seenVer：回源期间已有更高版本通知，结果必须丢弃：
// 条目落入 Stale 等待重新回源，并补记一次失效（它作废了被丢弃的结果）。
func (e *Entry) CommitFetch(p Payload, ttl int64, now int64) bool {
	if p.Ver.Newer(e.seenVer) {
		e.seenVer = p.Ver
	}
	if p.Ver.Before(e.seenVer) {
		e.state = Stale
		e.staleAt = 0 // 失效已在 NoteFetchingInvalidate 对应的通知中计数，不重复计
		return false
	}
	e.data = p.Data
	e.exists = p.Exists
	e.dataVer = p.Ver
	e.expire = now + ttl
	e.state = Valid
	e.loaded = true
	e.staleAt = 0
	return true
}

// AbortFetch 回源失败：绝不写入任何数据。
// 若回源期间收到过高版本通知，落入 Stale（可重新回源）并补记一次失效；
// 否则回到 Hole（若从未成功过）或 Stale（带着旧版本快照，也可立即重新回源）。
func (e *Entry) AbortFetch() {
	fresh := e.staleAt == 0
	e.staleAt = 0
	if !fresh {
		e.state = Stale
		return
	}
	if e.loaded {
		e.state = Stale
	} else {
		e.state = Hole
	}
}

// Snapshot 取条目快照（Data 为拷贝，调用方可安全持有）。
func (e *Entry) Snapshot() Snapshot {
	s := Snapshot{
		State:   e.state,
		Exists:  e.exists,
		DataVer: e.dataVer,
		SeenVer: e.seenVer,
		Expire:  e.expire,
		Fetches: e.fetches,
		Invalid: e.invalid,
		Loaded:  e.loaded,
	}
	if e.data != nil {
		s.Data = append([]byte(nil), e.data...)
	}
	return s
}
