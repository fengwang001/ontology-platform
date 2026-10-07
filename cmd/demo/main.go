// Command demo 在终端演示假设性历史预检：
// 钩子版本演进、权限继承调整、边界时刻切换、前后置阶段与审计链。
//
// 运行：go run ./cmd/demo
package main

import (
	"fmt"

	"ontology/hypcheck"
)

func main() {
	audit := hypcheck.NewMemoryAuditor()
	e := hypcheck.NewEngine(hypcheck.Config{Auditor: audit})

	// t=0 定义动作类型 transfer：参数 to（字符串，必填），意图写键 balance。
	e.DefineType(0, "transfer", hypcheck.TypeVersion{
		Schema: hypcheck.Schema{Fields: []hypcheck.Field{
			{Name: "to", Type: hypcheck.FieldType(hypcheck.KindStr), Required: true},
		}},
		Effects: []hypcheck.EffectDecl{{Key: "balance", Param: "to"}},
	})

	// 身份（t=1）；alice 从 t=5 起继承 org-a；org-a 在 t=6 被授予 transfer。
	e.UpsertPrincipal(1, "alice", true)
	e.UpsertPrincipal(2, "org-a", true)
	e.ToggleEdge(5, "org-a", "alice", true)
	e.ToggleGrant(6, "org-a", "transfer", true)

	// 前置钩子 v1（[7,10) 生效）：to=="frozen" 拒绝；t=10 切换 v2：to=="blocked" 拒绝。
	e.PublishHook(7, "deny-target", hypcheck.PhasePre, hypcheck.HookVersion{Version: "v1",
		Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyParamEqual, Param: "to", Value: hypcheck.StrValue("frozen")}})
	e.BindHook(8, "transfer", "deny-target", hypcheck.PhasePre)
	e.PublishHook(10, "deny-target", hypcheck.PhasePre, hypcheck.HookVersion{Version: "v2",
		Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyParamEqual, Param: "to", Value: hypcheck.StrValue("blocked")}})

	show := func(at hypcheck.Timestamp, to string) {
		r := e.Precheck(hypcheck.PrecheckRequest{
			At: at, TypeID: "transfer", Caller: "alice",
			Params: hypcheck.Params{"to": hypcheck.StrValue(to)}},
		)
		hookVer := "-"
		if len(r.Hooks) > 0 {
			hookVer = r.Hooks[0].Version
		}
		grantedBy := ""
		if r.PermTrace != nil {
			grantedBy = r.PermTrace.GrantedBy
		}
		fmt.Printf("at=%-3d to=%-8q verdict=%-7s hook=%s grant=%q probes=%d",
			at, to, r.Verdict, hookVer, grantedBy, r.Probes)
		if r.Verdict == hypcheck.VerdictError {
			fmt.Printf(" error=%s", r.ErrorClass)
		}
		if len(r.PreFailures) > 0 {
			fmt.Printf(" pre=%d", len(r.PreFailures))
		}
		if len(r.Effects) > 0 {
			fmt.Printf(" effects=%v", r.Effects)
		}
		fmt.Println()
	}

	fmt.Println("== 边界两侧钩子版本（t=10 取新侧 v2）==")
	show(9, "frozen")  // v1 拒绝
	show(9, "blocked") // v1 下允许
	show(10, "frozen") // v2 下允许
	show(10, "blocked")

	fmt.Println("== 权限继承边界（t=6 才经 org-a 获得授权）==")
	show(4, "alice") // 未授权 -> deny
	show(6, "alice") // 已授权 -> allowed + 意图

	fmt.Println("== 早于类型定义时刻 -> E1 ==")
	r := e.Precheck(hypcheck.PrecheckRequest{At: -1, TypeID: "transfer", Caller: "alice"})
	fmt.Printf("verdict=%s error=%s\n", r.Verdict, r.ErrorClass)

	fmt.Println("== 审计哈希链 ==")
	fmt.Printf("records=%d verify=%v\n", len(audit.Entries()), audit.Verify())
}
