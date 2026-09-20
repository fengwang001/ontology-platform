// demo 逐项演练唯一约束检查器：规范化开关、NULL 语义、
// 冲突报告、批量延迟检查与并发安全性。不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology"
)

var passed, failed int

func check(ok bool, format string, args ...any) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, args...))
}

func nameRec(pk, name string) ontology.Record {
	return ontology.Record{PK: pk, Values: map[string]*string{"name": ontology.Str(name)}}
}

func uniq(norm ontology.Normalizer, nullsEqual bool) *ontology.Checker {
	return ontology.NewChecker(norm,
		ontology.Constraint{Name: "uniq_name", Props: []string{"name"}, NullsEqual: nullsEqual})
}

func demoNormalizeCombos() {
	combos := []struct {
		label string
		norm  ontology.Normalizer
		want  bool // 是否冲突
	}{
		{"裁剪=关 折叠=关", ontology.Normalizer{TrimSpace: false, CaseFold: false}, false},
		{"裁剪=开 折叠=关", ontology.Normalizer{TrimSpace: true, CaseFold: false}, false},
		{"裁剪=关 折叠=开", ontology.Normalizer{TrimSpace: false, CaseFold: true}, false},
		{"裁剪=开 折叠=开", ontology.Normalizer{TrimSpace: true, CaseFold: true}, true},
	}
	for _, cb := range combos {
		c := uniq(cb.norm, false)
		_ = c.Add(nameRec("pk1", "  Alice"))
		conflict := c.Add(nameRec("pk2", "alice")) != nil
		check(conflict == cb.want, "开关组合[%s]: \"  Alice\" 与 \"alice\" 冲突=%v", cb.label, conflict)
	}
}

func demoStoredValue() {
	c := uniq(ontology.Normalizer{TrimSpace: true, CaseFold: true}, false)
	raw := "  Àlìçé ß  "
	_ = c.Add(nameRec("pk1", raw))
	rec, _ := c.Get("pk1")
	check(*rec.Values["name"] == raw, "读回值与写入值逐字节相同: %q", *rec.Values["name"])
}

func demoNullSemantics() {
	def := uniq(ontology.Normalizer{}, false)
	_ = def.Add(ontology.Record{PK: "n1", Values: map[string]*string{"name": nil}})
	err := def.Add(ontology.Record{PK: "n2", Values: map[string]*string{"name": nil}})
	check(err == nil, "NULL 默认模式: 两个 NULL 不冲突")
	eq := uniq(ontology.Normalizer{}, true)
	_ = eq.Add(ontology.Record{PK: "n1", Values: map[string]*string{"name": nil}})
	err = eq.Add(ontology.Record{PK: "n2", Values: map[string]*string{"name": nil}})
	check(err != nil, "NULL 相等模式: 两个 NULL 冲突")
	c := uniq(ontology.Normalizer{}, true)
	_ = c.Add(ontology.Record{PK: "null", Values: map[string]*string{"name": nil}})
	err = c.Add(nameRec("empty", ""))
	check(err == nil, "空字符串与 NULL 是不同的值: 共存成功")
}

func demoConflictReport() {
	c := uniq(ontology.Normalizer{TrimSpace: true, CaseFold: true}, false)
	_ = c.Add(nameRec("pk1", "ALICE"))
	err := c.Add(nameRec("pk2", "  Alice"))
	var ce *ontology.ConflictError
	ok := errors.As(err, &ce) && ce.Constraint == "uniq_name" && ce.ExistingPK == "pk1"
	check(ok, "冲突报告: 你输入的 %q 与已有的 %q(%s) 冲突, 规范化键 {%s}",
		*ce.Incoming["name"], *ce.Existing["name"], ce.ExistingPK, ce.Key)
}

func demoBatch() {
	c := uniq(ontology.Normalizer{TrimSpace: true, CaseFold: true}, false)
	_ = c.Add(nameRec("old", "Alice"))
	err := c.ApplyBatch([]ontology.Op{
		ontology.Delete("old"),
		ontology.Insert(nameRec("new", "  ALICE  ")),
	})
	check(err == nil, "批内先删后插同键记录: 成功")
	err = c.ApplyBatch([]ontology.Op{
		ontology.Insert(nameRec("a", "Bob")),
		ontology.Insert(nameRec("b", " bob ")),
	})
	var be *ontology.BatchConflictError
	ok := errors.As(err, &be)
	check(ok, "批内双插同键记录: 拒绝, 第%d条与第%d条冲突", be.First, be.Second)
}

func demoConcurrency() {
	c := uniq(ontology.Normalizer{TrimSpace: true, CaseFold: true}, false)
	const n = 64
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.Add(nameRec(fmt.Sprintf("pk-%d", i), "alICE"))
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	check(successes == 1 && c.SelfCheck() == nil,
		"并发插入 %d 条同键记录: 恰好 %d 条成功, 索引自检通过", n, successes)
}

func main() {
	demoNormalizeCombos()
	demoStoredValue()
	demoNullSemantics()
	demoConflictReport()
	demoBatch()
	demoConcurrency()
	fmt.Printf("总计: %d 项通过, %d 项失败\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
