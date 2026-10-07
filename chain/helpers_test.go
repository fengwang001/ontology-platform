package chain

import "fmt"

// 测试夹具：注册一组基础动作；父动作在各测试用例中按需内联组装。
//
//	set(obj,k,v) 无条件写单个属性
//	increment(obj,by) 前置要求对象在当前视图可见；n += by；后置校验一致性
//	failPre  前置恒失败；failPost 后置恒失败；boomEffect 效果返回 error
//	descend(depth) depth>0 时以 depth-1 触发自身（输入不同，放行）
//	loopSame(x) 始终以相同输入触发自身（自我触发，应被拒绝）
func buildTestRegistry() *Registry {
	r := NewRegistry()

	mustReg(r, &Action{
		Name: "set",
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			p := c.Input()
			return []WriteOp{{ObjectID: p["obj"].(string),
				Upsert: map[string]any{p["k"].(string): p["v"]}}}, nil, "set attr", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "always" },
	})

	mustReg(r, &Action{
		Name: "increment",
		Pre: func(c ExecContext) (bool, string) {
			attrs, ok := c.State().Get(c.Input()["obj"].(string))
			if !ok {
				return false, "object not visible"
			}
			return true, fmt.Sprintf("visible n=%d", num(attrs["n"]))
		},
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			p := c.Input()
			attrs, _ := c.State().Get(p["obj"].(string))
			next := num(attrs["n"]) + num(p["by"])
			return []WriteOp{{ObjectID: p["obj"].(string), Upsert: map[string]any{"n": next}}},
				map[string]any{"next": next}, "increment", nil
		},
		Post: func(c ExecContext, out map[string]any) (bool, string) {
			attrs, _ := c.State().Get(c.Input()["obj"].(string))
			if num(attrs["n"]) != num(out["next"]) {
				return false, "post mismatch"
			}
			return true, "consistent"
		},
	})

	mustReg(r, &Action{
		Name:   "failPre",
		Pre:    func(ExecContext) (bool, string) { return false, "forced pre failure" },
		Effect: func(ExecContext) ([]WriteOp, map[string]any, string, error) { return nil, nil, "", nil },
		Post:   func(ExecContext, map[string]any) (bool, string) { return true, "always" },
	})

	mustReg(r, &Action{
		Name: "failPost",
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			return []WriteOp{{ObjectID: c.Input()["obj"].(string),
				Upsert: map[string]any{"postfail": true}}}, nil, "write", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return false, "forced post failure" },
	})

	mustReg(r, &Action{
		Name: "boomEffect",
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			return []WriteOp{{ObjectID: c.Input()["obj"].(string),
				Upsert: map[string]any{"boom": true}}}, nil, "boom", errBoom
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "always" },
	})

	mustReg(r, &Action{
		Name: "descend",
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			return []WriteOp{{ObjectID: "chain",
				Upsert: map[string]any{"depth": num(c.Input()["depth"])}}}, nil, "mark", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "always" },
		Children: []ChildSpec{{
			Name:     "descend",
			Critical: true,
			When:     func(p Params, _ func(string) (any, bool)) bool { return num(p["depth"]) > 0 },
			Bind: func(p Params, _ func(string) (any, bool)) Params {
				return Params{"depth": num(p["depth"]) - 1}
			},
		}},
	})

	mustReg(r, &Action{
		Name: "loopSame",
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			return []WriteOp{{ObjectID: "loop", Upsert: map[string]any{"x": num(c.Input()["x"])}}},
				nil, "mark", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "always" },
		Children: []ChildSpec{{
			Name:     "loopSame",
			Critical: true,
			When:     func(Params, func(string) (any, bool)) bool { return true },
			Bind: func(p Params, _ func(string) (any, bool)) Params {
				return Params{"x": num(p["x"])}
			},
		}},
	})

	return r
}

func mustReg(r *Registry, a *Action) {
	if err := r.Register(a); err != nil {
		panic(err)
	}
}

func num(v any) int {
	n, _ := v.(int)
	return n
}

type sentinelErr string

func (e sentinelErr) Error() string { return string(e) }

const errBoom = sentinelErr("boom")
