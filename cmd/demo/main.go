// Command demo 演示三方合并冲突检测器的各项判定。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"

	"ontology/conflict"
	"ontology/delta"
	"ontology/doc"
	"ontology/merge3"
	"ontology/serialize"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	status := "OK"
	if !ok {
		status = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %s: %s\n", status, name, detail)
}

func main() {
	s1 := doc.Set{"k": {"f": doc.Str("")}}
	s2 := doc.Set{"k": {}}
	check("doc-empty-vs-absent", !bytes.Equal(doc.Canonical(s1), doc.Canonical(s2)),
		"空串值与字段不存在在编码上可区分")

	bad := delta.New()
	bad.Deleted["k1"] = true
	bad.Changed["k1"] = map[string]delta.FieldChange{}
	err := bad.Validate()
	check("delta-contradictory", errors.Is(err, delta.ErrContradictory),
		"同侧删改矛盾被检出: "+errText(err))

	check("conflict-kinds-distinct",
		conflict.DeleteVsModify.String() != conflict.FieldValue.String() &&
			conflict.AddAdd.String() != conflict.TypeMismatch.String(),
		"四种冲突类型名称可区分")

	checkMerge3()
	checkSerialize()

	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}

func checkSerialize() {
	s := doc.Set{"k": rec("f", doc.Str("v"))}
	rep := conflict.Report{List: []conflict.Conflict{
		{Kind: conflict.DeleteVsModify, Key: "k2", RightOK: true},
	}}
	full := serialize.Marshal(s, rep)
	_, _, errH := serialize.Unmarshal(full[:5])
	_, _, errR := serialize.Unmarshal(full[:len(full)-5])
	_, _, errC := serialize.Unmarshal(full[:len(full)-1])
	check("truncate-classes",
		errors.Is(errH, serialize.ErrHeaderIncomplete) &&
			errors.Is(errR, serialize.ErrRecordIncomplete) &&
			errors.Is(errC, serialize.ErrCRCMismatch),
		"三类截断分类各一例: 头部/记录/CRC")
}

func rec(pairs ...any) doc.Record {
	r := doc.Record{}
	for i := 0; i < len(pairs); i += 2 {
		r[pairs[i].(string)] = pairs[i+1].(doc.Value)
	}
	return r
}

func checkMerge3() {
	S, N := doc.Str, doc.Num

	_, rep, _ := merge3.Merge(doc.Set{"k": rec("f", N(1))}, doc.Set{"k": rec("f", N(2))}, doc.Set{"k": rec("f", N(2))})
	check("same-value-both-sides", rep.Empty(), "两侧改成相同值无冲突")

	got, rep, _ := merge3.Merge(doc.Set{"k": rec("f", N(1))}, doc.Set{}, doc.Set{"k": rec("f", N(2))})
	ok := len(rep.List) == 1 && rep.List[0].Kind == conflict.DeleteVsModify &&
		rep.List[0].Kind != conflict.FieldValue && len(got) == 0
	check("delete-vs-modify", ok, "左删右改判为 DeleteVsModify 且与 FieldValue 可区分")

	anc := doc.Set{"k": rec("a", N(1), "b", N(1), "c", N(1))}
	got, rep, _ = merge3.Merge(anc,
		doc.Set{"k": rec("a", N(2), "b", N(1), "c", N(1))},
		doc.Set{"k": rec("a", N(1), "b", N(3), "c", N(1))})
	g := got["k"]
	ok = rep.Empty() && g["a"] == N(2) && g["b"] == N(3) && g["c"] == N(1)
	check("different-fields", ok, "改不同字段两者都生效且第三字段保持祖先值")

	_, rep, _ = merge3.Merge(doc.Set{"k": rec("f", N(1))}, doc.Set{}, doc.Set{})
	check("both-deleted", rep.Empty(), "两侧同删无冲突")

	mk := func(rng *rand.Rand) doc.Set {
		s := doc.Set{}
		ks := []string{"a", "b", "c", "d", "e"}
		rng.Shuffle(len(ks), func(i, j int) { ks[i], ks[j] = ks[j], ks[i] })
		for _, k := range ks {
			s[k] = rec("x", N(1), "y", S("v"))
		}
		return s
	}
	var ref []byte
	same := true
	for i := 0; i < 20; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		m, r, _ := merge3.Merge(mk(rng), mk(rng), mk(rng))
		b := append(doc.Canonical(m), []byte(r.String())...)
		if i == 0 {
			ref = b
		} else if !bytes.Equal(b, ref) {
			same = false
		}
	}
	check("determinism-20-shuffles", same, "打乱构造顺序 20 次输出逐字节相同")

	const n = 50000
	big := make(doc.Set, n)
	for i := 0; i < n; i++ {
		big[fmt.Sprintf("k%06d", i)] = rec("f", N(float64(i)))
	}
	_, _, st := merge3.Merge(big, big, big)
	check("lookup-bound", st.Lookups <= 4*n,
		fmt.Sprintf("查找次数 %d <= 上界 %d", st.Lookups, 4*n))
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
