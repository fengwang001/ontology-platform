package bitemporal

import "time"

// resolveObject 判定一个对象在 (validAt, asOf) 下的可见性。
// 版本按写入时间升序保存；取 WrittenAt <= asOf 的最后一条作为「截至该写入
// 时间点已落定的最新记录」，不存在已落定记录时取最早版本（此时其写入时间
// 必然晚于 asOf）用于判定覆盖。
func resolveObject(versions []ObjectRecord, validAt, asOf time.Time) (ObjectRecord, Status) {
	if len(versions) == 0 {
		return ObjectRecord{}, StatusNotEstablished
	}
	settled := -1
	for i := range versions {
		if !versions[i].WrittenAt.After(asOf) {
			settled = i
		}
	}
	rec := versions[0]
	if settled >= 0 {
		rec = versions[settled]
		if rec.Valid.Contains(validAt) {
			return rec, StatusVisible
		}
	}
	// 已落定记录不覆盖（或尚无落定记录）：
	// 若存在任意版本（哪怕写入时间晚于 asOf）覆盖 validAt，
	// 则事实「已建立但对此查询尚不可见」，否则「此刻未建立」。
	for _, v := range versions {
		if v.Valid.Contains(validAt) {
			return rec, StatusNotYetVisible
		}
	}
	return rec, StatusNotEstablished
}

// resolveLink 判定一个链接在 (validAt, asOf) 下的可见性，语义同 resolveObject。
func resolveLink(versions []LinkRecord, validAt, asOf time.Time) (LinkRecord, Status) {
	if len(versions) == 0 {
		return LinkRecord{}, StatusNotEstablished
	}
	settled := -1
	for i := range versions {
		if !versions[i].WrittenAt.After(asOf) {
			settled = i
		}
	}
	rec := versions[0]
	if settled >= 0 {
		rec = versions[settled]
		if rec.Valid.Contains(validAt) {
			return rec, StatusVisible
		}
	}
	for _, v := range versions {
		if v.Valid.Contains(validAt) {
			return rec, StatusNotYetVisible
		}
	}
	return rec, StatusNotEstablished
}

// combineStatus 汇总一跳的三方状态。
// 约定：只要任意一方为 NotEstablished，整跳归类为 NotEstablished；
// 否则只要任意一方为 NotYetVisible，归类为 NotYetVisible。
func combineStatus(statuses ...Status) Status {
	for _, st := range statuses {
		if st == StatusNotEstablished {
			return StatusNotEstablished
		}
	}
	for _, st := range statuses {
		if st == StatusNotYetVisible {
			return StatusNotYetVisible
		}
	}
	return StatusVisible
}
