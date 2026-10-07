// precheck-demo 演示动作假设性重新预检子系统：
// 在同一动作类型上演化钩子版本、调整权限继承，然后对不同历史时刻发起
// 只读预检，观察“当时生效版本集合/继承快照”如何决定允许或拒绝。
package main

import (
	"encoding/json"
	"fmt"

	"ontology/precheck"
)

// quotaHook 是一个真实的钩子实现：参数 amount 超过当时配额即拒绝；
// 前置阶段把折算后的金额放入 scratch，后置阶段据此产出写入意图。
type quotaHook struct{ id string }

func (h *quotaHook) ID() string { return h.id }

func (h *quotaHook) Pre(ctx *precheck.PreContext) error {
	v, _ := ctx.Param("amount")
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case int:
		n = float64(x)
	case int64:
		n = float64(x)
	}
	if n > 100 {
		return &precheck.HookError{Code: "amount_too_large", Message: "amount exceeds quota 100"}
	}
	ctx.SetScratch("effective_amount", n)
	return nil
}

func (h *quotaHook) Post(ctx *precheck.PostContext) ([]precheck.Intent, error) {
	key, _ := ctx.Param("object_key")
	amount, _ := ctx.Scratch("effective_amount")
	return []precheck.Intent{{
		Op:        "upsert",
		ObjectKey: key.(string),
		Value:     map[string]any{"approved_amount": amount},
		Source:    h.id,
	}}, nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	reg := precheck.NewRegistry()
	reg.Register(&quotaHook{id: "quota-v2"})
	engine := precheck.NewEngine(reg)

	must(engine.Apply(precheck.Event{
		At: 10, Kind: precheck.EvTypeSchema, ActionType: "Transfer",
		Schema: &precheck.ParamSchema{
			Version: 10, Required: []string{"object_key", "amount"},
			Types:              map[string]string{"object_key": "string", "amount": "number"},
			RequiredPermission: "can-transfer",
		},
	}))
	must(engine.Apply(precheck.Event{At: 10, Kind: precheck.EvUserAdded, User: "alice"}))

	// t=10：alice 通过组 admins 继承 can-transfer；当时尚无钩子（v10 空集合）。
	must(engine.Apply(precheck.Event{At: 10, Kind: precheck.EvEdgeAdded, From: "alice", To: "admins"}))
	must(engine.Apply(precheck.Event{At: 10, Kind: precheck.EvEdgeAdded, From: "admins", To: "can-transfer"}))
	must(engine.Apply(precheck.Event{At: 10, Kind: precheck.EvHookSet, ActionType: "Transfer",
		HookSet: &precheck.HookSet{Version: 10}}))

	// t=20：新增前置+后置配额钩子版本。
	must(engine.Apply(precheck.Event{At: 20, Kind: precheck.EvHookSet, ActionType: "Transfer",
		HookSet: &precheck.HookSet{Version: 20,
			Pre: []precheck.HookRef{{ID: "quota-v2"}}, Post: []precheck.HookRef{{ID: "quota-v2"}}}}))

	// t=30：继承关系调整——alice 离开 admins，权限不再可达。
	must(engine.Apply(precheck.Event{At: 30, Kind: precheck.EvEdgeRemoved, From: "alice", To: "admins"}))

	show := func(label string, req precheck.PrecheckRequest) {
		res, err := engine.Precheck(req)
		if err != nil {
			panic(err)
		}
		raw, _ := json.MarshalIndent(map[string]any{
			"label":   label,
			"request": req,
			"result":  res,
		}, "", "  ")
		fmt.Println(string(raw))
	}

	params := map[string]any{"object_key": "acct-7", "amount": 50}

	// 边界时刻 t=10：旧版本（无钩子），通过继承有权限 -> 允许。
	show("t=10 old hookset, inherited permission", precheck.PrecheckRequest{
		At: 10, ActionType: "Transfer", Caller: "alice", Params: params, Aggregation: precheck.CollectAll})

	// t=20：新版本钩子生效，金额合法 -> 允许并给出意图。
	show("t=20 new hookset active", precheck.PrecheckRequest{
		At: 25, ActionType: "Transfer", Caller: "alice",
		Params: map[string]any{"object_key": "acct-7", "amount": 150}, Aggregation: precheck.CollectAll})

	// t=30：继承被切断 -> 前置授权门失败，钩子不演算。
	show("t=30 inheritance revoked", precheck.PrecheckRequest{
		At: 30, ActionType: "Transfer", Caller: "alice", Params: params, Aggregation: precheck.CollectAll})

	fmt.Printf("\naudit entries written: %d\n", len(engine.Audit().Entries()))
}
