package delta

import (
	"sort"

	"ontology/doc"
)

// Build 计算 side 相对祖先 ancestor 的变更集。
func Build(ancestor, side doc.Set) (*Set, error) {
	d := &Set{entries: map[string]*Entry{}}
	var keys []string
	seen := map[string]bool{}
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range ancestor {
		add(k)
	}
	for k := range side {
		add(k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ar, aOk := ancestor[k]
		sr, sOk := side[k]
		switch {
		case !aOk && !sOk:
			continue
		case !aOk && sOk:
			rec := make(doc.Record, len(sr))
			for f, v := range sr {
				rec[f] = v
			}
			d.entries[k] = &Entry{Key: k, Kind: KindAdded, Record: rec}
		case aOk && !sOk:
			d.entries[k] = &Entry{Key: k, Kind: KindDeleted}
		default:
			if e := diffRecord(k, ar, sr); e != nil {
				d.entries[k] = e
			}
		}
	}
	d.order = keys[:0]
	for k := range d.entries {
		d.order = append(d.order, k)
	}
	sort.Strings(d.order)
	return d, nil
}

func diffRecord(key string, ar, sr doc.Record) *Entry {
	fields := map[string]FieldChange{}
	var fkeys []string
	seen := map[string]bool{}
	for f := range ar {
		seen[f] = true
		fkeys = append(fkeys, f)
	}
	for f := range sr {
		if !seen[f] {
			fkeys = append(fkeys, f)
		}
	}
	for _, f := range fkeys {
		av, aOk := ar[f]
		sv, sOk := sr[f]
		switch {
		case !aOk && sOk:
			fields[f] = FieldChange{New: sv, Op: OpAdded}
		case aOk && !sOk:
			fields[f] = FieldChange{Old: av, Op: OpRemoved, HadOld: true}
		case aOk && sOk && !av.Equal(sv):
			fields[f] = FieldChange{Old: av, New: sv, Op: OpChanged, HadOld: true}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &Entry{Key: key, Kind: KindModified, Fields: fields}
}

// Validate 检查外部构造/标注的变更集是否自相矛盾：同一键不能既删除又新增/修改。
func Validate(entries []*Entry) error {
	bad := map[string]bool{}
	state := map[string]Kind{}
	for _, e := range entries {
		prev, ok := state[e.Key]
		if ok && prev != e.Kind &&
			(prev == KindDeleted || e.Kind == KindDeleted) {
			bad[e.Key] = true
		}
		state[e.Key] = e.Kind
	}
	for k := range bad {
		return &ContradictionError{Key: k}
	}
	return nil
}
