package ontologytest

import (
	"fmt"
	"sort"

	"ontology/ontology"
)

func statusName(s ontology.IndexStatus) string {
	switch s {
	case ontology.StateIndexable:
		return "indexable"
	case ontology.StateMissingSource:
		return "missing"
	case ontology.StateNotUnique:
		return "not-unique"
	case ontology.StateNoValue:
		return "no-value"
	default:
		return "?"
	}
}

func naiveStatusName(s StatusKind) string {
	switch s {
	case StIndexable:
		return "indexable"
	case StMissingSource:
		return "missing"
	case StNotUnique:
		return "not-unique"
	case StNoValue:
		return "no-value"
	default:
		return "?"
	}
}

func (p modelPair) assertAll(step int) error {
	// 1) 逐实例状态对拍（单层 + 两级传递 + 全部不可索引状态）。
	cityIDs := make([]ID, 0)
	storeIDs := make([]ID, 0)
	for id, ins := range p.nm.inst {
		switch TypeName(ins.typ) {
		case tCity:
			cityIDs = append(cityIDs, id)
		case tStore:
			storeIDs = append(storeIDs, id)
		}
	}
	sort.Slice(cityIDs, func(i, j int) bool { return cityIDs[i] < cityIDs[j] })
	sort.Slice(storeIDs, func(i, j int) bool { return storeIDs[i] < storeIDs[j] })

	for _, id := range cityIDs {
		got, err := p.st.Resolve(oid(id), opn(pRegion))
		if err != nil {
			return fmt.Errorf("resolve city %s: %w", id, err)
		}
		want := p.nm.Evaluate(id, pRegion)
		if e := compareState(string(id), statusName(got.Status), naiveStatusName(want.Status),
			got.Value.Val, string(want.Value)); e != nil {
			return fmt.Errorf("step %d city regionCode: %w", step, e)
		}
	}
	for _, id := range storeIDs {
		got, err := p.st.Resolve(oid(id), opn(pCityOf))
		if err != nil {
			return fmt.Errorf("resolve store %s: %w", id, err)
		}
		want := p.nm.Evaluate(id, pCityOf)
		if e := compareState(string(id), statusName(got.Status), naiveStatusName(want.Status),
			got.Value.Val, string(want.Value)); e != nil {
			return fmt.Errorf("step %d store cityOf: %w", step, e)
		}
	}

	// 2) 完整索引桶对拍（等价于“索引查询在任意键值上都正确”）。
	if err := compareIndex(p, step, tCity, pRegion); err != nil {
		return err
	}
	if err := compareIndex(p, step, tStore, pCityOf); err != nil {
		return err
	}
	return nil
}

func compareState(id, gotStatus, wantStatus, gotVal, wantVal string) error {
	if gotStatus != wantStatus {
		return fmt.Errorf("instance %s status mismatch: got %s want %s",
			id, gotStatus, wantStatus)
	}
	if gotStatus == "indexable" && gotVal != wantVal {
		return fmt.Errorf("instance %s value mismatch: got %q want %q",
			id, gotVal, wantVal)
	}
	return nil
}

func compareIndex(p modelPair, step int, t TypeName, prop PropName) error {
	want := p.nm.FullIndex(t, prop)
	wantValues := map[Val]bool{}
	for v := range want {
		wantValues[v] = true
	}
	// 通过 QueryIndex 逐桶校验：覆盖朴素模型出现的全部键值，并补查不存在的键。
	allValues := map[string]bool{}
	for v := range wantValues {
		allValues[string(v)] = true
	}
	allValues["__absent__"] = true
	for v := range allValues {
		rows, err := p.st.QueryIndex(
			otn(t), opn(prop), v)
		if err != nil {
			return fmt.Errorf("step %d query (%s,%s=%s): %w", step, t, prop, v, err)
		}
		gotIDs := make([]string, 0, len(rows))
		for _, r := range rows {
			gotIDs = append(gotIDs, string(r.ID))
		}
		wantIDs := []string{}
		if v != "__absent__" {
			for _, id := range want[Val(v)] {
				wantIDs = append(wantIDs, string(id))
			}
		}
		if !equalStrings(gotIDs, wantIDs) {
			return fmt.Errorf("step %d index (%s,%s=%s) mismatch: got %v want %v",
				step, t, prop, v, gotIDs, wantIDs)
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
