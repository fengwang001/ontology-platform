package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/agg"
	"ontology/change"
)

type check struct {
	name string
	ok   bool
}

func report(cs []check) {
	pass := 0
	for _, c := range cs {
		tag := "OK  "
		if !c.ok {
			tag = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(cs))
	if pass != len(cs) {
		panic("demo checks failed")
	}
}

func main() {
	var cs []check

	// change：编解码往返（含空串分组、±0 归一化）与非法变更拒绝。
	src := change.Change{Op: change.Insert, Ver: 7,
		Rec: change.Record{ID: "r1", Group: "", Value: math.Copysign(0, -1)}}
	b, err := src.Encode()
	got, derr := func() (change.Change, error) {
		if err != nil {
			return change.Change{}, err
		}
		return change.Decode(b)
	}()
	okRound := derr == nil && got.Rec.Group == "" && got.Rec.Value == 0 &&
		!math.Signbit(got.Rec.Value) && got.Ver == 7
	_, errMiss := change.Change{Op: change.Insert, Ver: 1,
		Rec: change.Record{ID: "x", GroupMissing: true, Value: 1}}.Encode()
	_, errNaN := change.Change{Op: change.Insert, Ver: 1,
		Rec: change.Record{ID: "x", Group: "g", Value: math.NaN()}}.Encode()
	cs = append(cs, check{"change 编解码往返/空组/±0/拒绝缺失分组与NaN",
		okRound && errors.Is(errMiss, change.ErrMissingGroup) &&
			errors.Is(errNaN, change.ErrNaN)})

	// agg：Count/Sum 删除不需要成员；Min/Max/DistinctCount 需要；Min 删极值返回 false。
	wantNeed := map[agg.Kind]bool{
		agg.KCount: false, agg.KSum: false, agg.KMin: true,
		agg.KMax: true, agg.KDistinctCount: true,
	}
	okAgg := true
	for _, k := range agg.AllKinds() {
		a := k.New()
		if a.NeedMembersOnDelete() != wantNeed[k] {
			okAgg = false
		}
	}
	mn := &agg.Min{}
	mn.Add(1)
	mn.Add(2)
	if mn.Remove(1) {
		okAgg = false // 删到当前极值必须声明无法增量撤回
	}
	cs = append(cs, check{"agg 撤回能力声明正确，Min删极值需重算", okAgg})

	report(cs)
}
