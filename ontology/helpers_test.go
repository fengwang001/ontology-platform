package ontology

import "testing"

// 本文件提供各测试共用的领域夹具：账户对象与转账动作。

func accountID(i int) ObjectID {
	return ObjectID("acct-" + string(rune('a'+i)))
}

func seedAccounts(store *Store, n int, balance int64) {
	for i := 0; i < n; i++ {
		store.Seed(Object{
			ID:    accountID(i),
			Type:  "account",
			Props: map[string]any{"balance": balance},
		})
	}
}

// transferAction 构造标准转账动作：
// 前置：金额为正、余额充足；后置：双方余额非负。
func transferAction() ActionType {
	return ActionType{
		ID: "transfer",
		Preconditions: []PreCondition{
			{
				ID: "positive-amount",
				Eval: func(in PreInput) bool {
					return in.Params["amount"].(int64) > 0
				},
			},
			{
				ID: "sufficient-funds",
				Eval: func(in PreInput) bool {
					from, ok := in.State.GetObject(ObjectID(in.Params["from"].(string)))
					if !ok {
						return false
					}
					return from.Props["balance"].(int64) >= in.Params["amount"].(int64)
				},
			},
		},
		Postconditions: []PostCondition{
			{
				ID: "no-negative-balance",
				Eval: func(in PostInput) bool {
					from, ok1 := in.State.GetObject(ObjectID(in.Params["from"].(string)))
					to, ok2 := in.State.GetObject(ObjectID(in.Params["to"].(string)))
					if !ok1 || !ok2 {
						return false
					}
					return from.Props["balance"].(int64) >= 0 && to.Props["balance"].(int64) >= 0
				},
			},
		},
		Apply: func(ctx *ApplyContext) error {
			fromID := ObjectID(ctx.Params["from"].(string))
			toID := ObjectID(ctx.Params["to"].(string))
			amount := ctx.Params["amount"].(int64)
			from, _ := ctx.State.GetObject(fromID)
			to, _ := ctx.State.GetObject(toID)
			if err := ctx.Plan.Update(fromID, map[string]any{
				"balance": from.Props["balance"].(int64) - amount,
			}); err != nil {
				return err
			}
			return ctx.Plan.Update(toID, map[string]any{
				"balance": to.Props["balance"].(int64) + amount,
			})
		},
	}
}

// transferCall 构造一次转账调用。
func transferCall(id string, from, to ObjectID, amount int64) Call {
	return Call{
		ID:         id,
		ActionType: "transfer",
		Params:     map[string]any{"from": string(from), "to": string(to), "amount": amount},
		Targets:    []ObjectID{from, to},
	}
}

func mustRegister(t *testing.T, reg *Registry, at ActionType, opts ...RegisterOption) {
	t.Helper()
	if err := reg.Register(at, opts...); err != nil {
		t.Fatalf("register action %q: %v", at.ID, err)
	}
}

// balanceOf 读取账户余额。
func balanceOf(t *testing.T, store *Store, id ObjectID) int64 {
	t.Helper()
	obj, ok := store.GetObject(id)
	if !ok {
		t.Fatalf("object %q not found", id)
	}
	return obj.Props["balance"].(int64)
}
