package bitemporal

import "sort"

// NaiveStore 是独立维护全部历史记录、查询时逐条全量扫描的朴素参照实现。
// 它刻意不建立任何按实体或端点组织的索引：所有访问都是对全量记录的线性
// 扫描与现场分组。其唯一用途是在大量随机操作序列上与索引实现逐条比对，
// 从而以「另一种显然正确的写法」交叉验证生产实现。
type NaiveStore struct {
	objects []ObjectRecord
	links   []LinkRecord
}

// NewNaiveStore 创建朴素参照存储。
func NewNaiveStore() *NaiveStore {
	return &NaiveStore{}
}

// AppendObject 追加对象记录并校验写入时间严格递增。
func (n *NaiveStore) AppendObject(rec ObjectRecord) error {
	if err := validateObjectRecord(rec); err != nil {
		return err
	}
	var sameID []ObjectRecord
	for _, r := range n.objects {
		if r.ID == rec.ID {
			sameID = append(sameID, r)
		}
	}
	sort.Slice(sameID, func(i, j int) bool { return sameID[i].WrittenAt.Before(sameID[j].WrittenAt) })
	if len(sameID) > 0 {
		last := sameID[len(sameID)-1]
		if !rec.WrittenAt.After(last.WrittenAt) {
			return &WriteError{Message: "object record WrittenAt must be strictly later than the previous record"}
		}
		for _, v := range sameID {
			if v.VersionID == rec.VersionID {
				return &WriteError{Message: "duplicate object VersionID"}
			}
		}
	}
	n.objects = append(n.objects, rec)
	return nil
}

// AppendLink 追加链接记录并校验写入时间严格递增与端点一致。
func (n *NaiveStore) AppendLink(rec LinkRecord) error {
	if err := validateLinkRecord(rec); err != nil {
		return err
	}
	var sameID []LinkRecord
	for _, r := range n.links {
		if r.ID == rec.ID {
			sameID = append(sameID, r)
		}
	}
	sort.Slice(sameID, func(i, j int) bool { return sameID[i].WrittenAt.Before(sameID[j].WrittenAt) })
	if len(sameID) > 0 {
		first := sameID[0]
		if first.SourceID != rec.SourceID || first.TargetID != rec.TargetID {
			return &WriteError{Message: "link endpoints must be consistent across versions"}
		}
		last := sameID[len(sameID)-1]
		if !rec.WrittenAt.After(last.WrittenAt) {
			return &WriteError{Message: "link record WrittenAt must be strictly later than the previous record"}
		}
		for _, v := range sameID {
			if v.VersionID == rec.VersionID {
				return &WriteError{Message: "duplicate link VersionID"}
			}
		}
	}
	n.links = append(n.links, rec)
	return nil
}

// naiveView 把 NaiveStore 适配为 graphView；每次访问都全量线性扫描。
type naiveView struct{ n *NaiveStore }

func (v naiveView) objectVersions(id string) []ObjectRecord {
	var out []ObjectRecord
	for _, r := range v.n.objects {
		if r.ID == id {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WrittenAt.Before(out[j].WrittenAt) })
	return out
}

func (v naiveView) linkVersions(id string) []LinkRecord {
	var out []LinkRecord
	for _, r := range v.n.links {
		if r.ID == id {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WrittenAt.Before(out[j].WrittenAt) })
	return out
}

func (v naiveView) outLinks(id string) []LinkRecord {
	var out []LinkRecord
	for _, r := range v.n.links {
		if r.SourceID == id {
			out = append(out, r)
		}
	}
	return out
}

// AsOf 以全量逐条扫描方式执行与 Engine.AsOf 语义相同的查询。
func (n *NaiveStore) AsOf(q Query) (*Result, error) {
	return asOfView(naiveView{n: n}, q)
}
