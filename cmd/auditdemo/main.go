// auditdemo 端到端演示权限决策审计回放模块：
// 版本化规则 → 记录判定 → 回放（一致/误判/篡改）→ 纠正链 → 三态查询。
package main

import (
	"fmt"
	"os"
	"time"

	"ontology/audit"
)

func main() {
	logger := audit.NewJSONLLogger(os.Stdout)
	s := audit.New(audit.WithLogger(logger))
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	v1, _ := s.SubmitRuleVersion(audit.RuleSet{Rules: []audit.Rule{
		{Effect: audit.EffectAllow, Subjects: []string{"alice"}, Targets: []string{"doc-1"}, Actions: []string{"read"}},
	}})

	// 一次正确判定与一次误判（PDP 缺陷导致）。
	ok, _ := s.RecordAccess("alice", "doc-1", audit.Request{Action: "read"}, t0, v1, audit.DecisionAllow)
	bad, _ := s.RecordAccess("alice", "doc-1", audit.Request{Action: "read"}, t0.Add(time.Hour), v1, audit.DecisionDeny)

	r1, _, _ := s.Replay(ok)
	fmt.Println("replay ok  ->", r1)
	r2, _, _ := s.Replay(bad)
	fmt.Println("replay bad ->", r2)

	// 对误判追加纠正，并再次纠正。
	c1, _ := s.AppendCorrection(bad, "", audit.DecisionAllow, "PDP stale cache caused false deny")
	s.AppendCorrection(bad, c1, audit.DecisionAllow, "confirmed by security review")

	leg, _, _, _ := s.QueryLegality("alice", "doc-1", t0.Add(time.Hour))
	fmt.Println("legality of the misjudged access ->", leg)

	// 模拟存储层篡改：回放以可区分方式报告完整性破坏。
	s.UnsafeCorruptVersionContent(v1, func(rs *audit.RuleSet) {
		rs.Rules[0].Effect = audit.EffectDeny
	})
	r3, _, _ := s.Replay(ok)
	fmt.Println("replay after tamper ->", r3)

	if err := logger.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "log write error:", err)
		os.Exit(1)
	}
}
