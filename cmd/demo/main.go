package main

import (
	"errors"
	"fmt"

	"ontology"
)

func main() {
	st := ontology.NewStore()
	eng := ontology.NewEngine(st)
	registerActions(eng)

	pass, total := 0, 0
	check := func(name string, ok bool, detail string) {
		total++
		if ok {
			pass++
		}
		tag := "OK  "
		if !ok {
			tag = "FAIL"
		}
		fmt.Printf("%s %s: %s\n", tag, name, detail)
	}

	// 1) 成功提交：序号与影响清单。
	r, err := eng.Execute("openAccount", map[string]any{"id": "alice", "balance": 100})
	check("成功提交", err == nil && r != nil,
		fmt.Sprintf("seq=%d impacts=%v", seq(r), impactKinds(r)))

	// 2) 参数错误一次报全：缺必填、类型不符、未声明参数。
	_, err = eng.Execute("transfer", map[string]any{"to": "alice", "amount": "ten", "extra": 1})
	var pe ontology.ParamErrors
	errors.As(err, &pe)
	check("参数一次报全", err != nil && len(pe) == 3, fmt.Sprintf("共 %d 条问题", len(pe)))

	// 3) 钩子拒绝：第 2 个钩子，余额不足。
	_, _ = eng.Execute("openAccount", map[string]any{"id": "bob", "balance": 5})
	_, err = eng.Execute("transfer", map[string]any{"from": "bob", "to": "alice", "amount": 50})
	var hr *ontology.HookRejectError
	errors.As(err, &hr)
	check("钩子拒绝", hr != nil && hr.Index == 2, fmt.Sprintf("第%d钩子 理由=%q", idx(hr), reason(hr)))

	// 4) 中途失败：回滚后对象计数与开始前一致。
	beforeObj, beforeRel := st.ObjectCount(), st.RelationCount()
	_, err = eng.Execute("audit", map[string]any{"id": "ghost"})
	afterObj, afterRel := st.ObjectCount(), st.RelationCount()
	check("失败整体回滚", err != nil && beforeObj == afterObj && beforeRel == afterRel,
		fmt.Sprintf("Action前 对象=%d关系=%d -> 回滚后 对象=%d关系=%d",
			beforeObj, beforeRel, afterObj, afterRel))

	// 5) 嵌套：内层失败只回滚到保存点，外层继续。
	r5, err := eng.Execute("settle", map[string]any{"id": "carol"})
	_, hasAudit := st.GetObject("carol-audit")
	_, hasCarol := st.GetObject("carol")
	check("嵌套保存点", err == nil && !hasAudit && hasCarol,
		fmt.Sprintf("外层seq=%d carol存在=%v carol-audit残留=%v", seq(r5), hasCarol, hasAudit))

	// 6) 递归检测：完整调用链。
	_, err = eng.Execute("recurse", nil)
	var rc *ontology.RecursionError
	errors.As(err, &rc)
	check("递归被拒绝", rc != nil && len(rc.Chain) == 2,
		fmt.Sprintf("调用链=%v", chain(rc)))

	// 7) 执行日志区间查询；失败 Action 未占序号。
	recs := eng.Log().Range(1, 0)
	check("日志无空洞", eng.Log().LastSeq() == int64(len(recs)),
		fmt.Sprintf("lastSeq=%d 记录数=%d Range(2,3)=%d", eng.Log().LastSeq(), len(recs),
			len(eng.Log().Range(2, 3))))

	fmt.Printf("==== 总计: %d/%d ====\n", pass, total)
}

func seq(r *ontology.Record) int64 {
	if r == nil {
		return -1
	}
	return r.Seq
}

func impactKinds(r *ontology.Record) []ontology.ImpactKind {
	if r == nil {
		return nil
	}
	out := make([]ontology.ImpactKind, len(r.Impacts))
	for i, im := range r.Impacts {
		out[i] = im.Kind
	}
	return out
}

func idx(h *ontology.HookRejectError) int {
	if h == nil {
		return -1
	}
	return h.Index
}

func reason(h *ontology.HookRejectError) string {
	if h == nil {
		return ""
	}
	return h.Reason
}

func chain(r *ontology.RecursionError) []string {
	if r == nil {
		return nil
	}
	return r.Chain
}
