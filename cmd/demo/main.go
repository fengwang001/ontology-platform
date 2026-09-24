// Command demo 演示三方合并冲突检测器的各项判定。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"ontology/conflict"
	"ontology/delta"
	"ontology/doc"
	"ontology/merge3"
	"ontology/serialize"
)

var passed, total int

func check(name string, ok bool) {
	total++
	status := "OK"
	if ok {
		passed++
	} else {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
}

// shuffled 以第 seed 种插入顺序构造同一集合，用于确定性检查。
func shuffled(seed int64, keys []string, rec func(k string) doc.Record) doc.Set {
	perm := rand.New(rand.NewSource(seed)).Perm(len(keys))
	s := make(doc.Set, len(keys))
	for _, i := range perm {
		s[keys[i]] = rec(keys[i])
	}
	return s
}

// checkDocCanonical 判定：doc 规范化编码与构造顺序无关。
func checkDocCanonical() {
	keys := []string{"r1", "r2", "r3"}
	rec := func(k string) doc.Record {
		return doc.Record{"a": 1, "b": "x", "": k}
	}
	base := doc.Encode(shuffled(0, keys, rec))
	same := true
	for seed := int64(1); seed <= 20; seed++ {
		if !bytes.Equal(base, doc.Encode(shuffled(seed, keys, rec))) {
			same = false
		}
	}
	check("doc: canonical encoding independent of insertion order", same)
}

// checkContradictory 判定：同侧同键既删又改被 delta.Validate 检出。
func checkContradictory() {
	d := delta.Delta{
		"R": {Deleted: true, Set: map[string]any{"a": 1}},
	}
	err := delta.Validate(d)
	ok := errors.Is(err, delta.ErrContradictory) && strings.Contains(err.Error(), `"R"`)
	check("delta: contradictory delete+modify on same key detected", ok)
}

// checkConflictReport 判定：报告按(键,字段)排序且访问计数==冲突数。
func checkConflictReport() {
	cs := []conflict.Conflict{
		conflict.New("b", "f", conflict.FieldValue, 1, 2),
		conflict.New("a", "z", conflict.DeleteModify, nil, "v"),
		conflict.New("a", "y", conflict.AddAdd, 1, 2),
	}
	r1 := conflict.Report(cs)
	access := conflict.ReportAccess()
	rev := []conflict.Conflict{cs[2], cs[1], cs[0]}
	r2 := conflict.Report(rev)
	ok := access == len(cs) && r1 == r2 &&
		strings.Index(r1, `key="a" field="y"`) < strings.Index(r1, `key="a" field="z"`) &&
		strings.Index(r1, `key="a"`) < strings.Index(r1, `key="b"`)
	check("conflict: report sorted, access count == conflict count", ok)
}

// checkMerge3 判定：merge3 的四条核心规则与查找次数上界。
func checkMerge3() {
	// 两侧改成相同值：无冲突且结果为 2。
	m, cs := merge3.Merge(
		doc.Set{"R": {"f": 1}},
		doc.Set{"R": {"f": 2}},
		doc.Set{"R": {"f": 2}},
	)
	check("merge3: both sides set same value -> no conflict, result 2",
		len(cs) == 0 && m["R"]["f"] == 2)

	// 左删右改：冲突且类别为 DeleteModify，可与 FieldValue 区分。
	_, cs = merge3.Merge(
		doc.Set{"R": {"f": 1}},
		doc.Set{},
		doc.Set{"R": {"f": 9}},
	)
	check("merge3: left delete vs right modify -> DeleteModify conflict",
		len(cs) == 1 && cs[0].Kind == conflict.DeleteModify && cs[0].Kind != conflict.FieldValue)

	// 改不同字段：a、b 都生效，c 保持祖先值。
	m, cs = merge3.Merge(
		doc.Set{"R": {"a": 1, "b": 1, "c": 1}},
		doc.Set{"R": {"a": 2, "b": 1, "c": 1}},
		doc.Set{"R": {"a": 1, "b": 3, "c": 1}},
	)
	check("merge3: different fields both apply, third keeps ancestor",
		len(cs) == 0 && m["R"]["a"] == 2 && m["R"]["b"] == 3 && m["R"]["c"] == 1)

	// 两侧同删：无冲突，记录消失。
	m, cs = merge3.Merge(doc.Set{"R": {"f": 1}}, doc.Set{}, doc.Set{})
	_, exists := m["R"]
	check("merge3: both sides delete -> no conflict, record gone",
		len(cs) == 0 && !exists)

	// 查找次数上界：3 个各 5 万键的集合，<= 4 * 键并集大小。
	const n = 50000
	mk := func(v int) doc.Set {
		s := make(doc.Set, n)
		for i := 0; i < n; i++ {
			s[fmt.Sprintf("k%06d", i)] = doc.Record{"f": v}
		}
		return s
	}
	merge3.Merge(mk(1), mk(2), mk(3))
	lookups, bound := merge3.LookupCount(), 4*n
	check(fmt.Sprintf("merge3: lookups %d <= 4*union %d", lookups, bound),
		lookups <= bound)
}

// checkDeterminism 判定：三集合插入顺序打乱 20 次，序列化输出逐字节相同。
func checkDeterminism() {
	keys := []string{"k1", "k2", "k3", "k4", "k5"}
	build := func(seed int64, f func(k string) (doc.Record, bool)) doc.Set {
		perm := rand.New(rand.NewSource(seed)).Perm(len(keys))
		s := make(doc.Set)
		for _, i := range perm {
			if rec, ok := f(keys[i]); ok {
				s[keys[i]] = rec
			}
		}
		return s
	}
	anc := func(k string) (doc.Record, bool) { return doc.Record{"a": 1, "b": "x"}, true }
	left := func(k string) (doc.Record, bool) {
		if k == "k4" {
			return nil, false
		}
		return doc.Record{"a": 2, "b": "x"}, true
	}
	right := func(k string) (doc.Record, bool) {
		return doc.Record{"a": 1, "b": "y"}, k != "k5"
	}
	var base []byte
	same := true
	for seed := int64(0); seed < 20; seed++ {
		m, cs := merge3.Merge(build(seed, anc), build(seed+100, left), build(seed+200, right))
		out := serialize.Encode(m, cs)
		if seed == 0 {
			base = out
		} else if !bytes.Equal(base, out) {
			same = false
		}
	}
	check("determinism: 20 shuffled constructions byte-identical", same)
}

// checkSerialize 判定：三类截断各一例，均可 errors.Is 区分。
func checkSerialize() {
	m := doc.Set{"R": {"f": 1}}
	cs := []conflict.Conflict{conflict.New("R", "f", conflict.FieldValue, 1, 2)}
	full := serialize.Encode(m, cs)
	_, _, errHeader := serialize.Decode(full[:2])
	_, _, errRecord := serialize.Decode(full[:9])
	_, _, errCRC := serialize.Decode(full[:len(full)-1])
	ok := errors.Is(errHeader, serialize.ErrHeaderIncomplete) &&
		errors.Is(errRecord, serialize.ErrRecordIncomplete) &&
		errors.Is(errCRC, serialize.ErrCRCMismatch)
	check("serialize: header/record/crc truncation classes", ok)
}

func main() {
	checkDocCanonical()
	checkContradictory()
	checkConflictReport()
	checkMerge3()
	checkDeterminism()
	checkSerialize()
	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		panic("demo checks failed")
	}
}
