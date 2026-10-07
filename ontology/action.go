package ontology

import "fmt"

// Args 是一次动作调用的输入参数。
type Args map[string]any

// Condition 是前置/后置条件，针对某个求值视图检查。
type Condition struct {
	Name  string
	Check func(v *View) error
}

// CallSpec 静态声明一次嵌套调用。
type CallSpec struct {
	ID       string // 缺省等于 Action
	Action   string
	Critical bool
}

// OutputRef 声明对某次嵌套调用输出的依赖。
type OutputRef struct {
	Call   string // CallSpec.ID
	Output string
}

// Action 是一个可注册的动作定义。
type Action struct {
	Name           string
	Preconditions  []Condition
	Postconditions []Condition
	Calls          []CallSpec
	Consumes       []OutputRef
	Run            func(ctx *Context) error
}

// declares 报告一次动态发起的嵌套调用是否与静态声明一致。
func (a *Action) declares(action string, critical bool) bool {
	for _, c := range a.Calls {
		if c.Action == action && c.Critical == critical {
			return true
		}
	}
	return false
}

func (a *Action) callSpec(id string) (CallSpec, bool) {
	for _, c := range a.Calls {
		effective := c.ID
		if effective == "" {
			effective = c.Action
		}
		if effective == id {
			return c, true
		}
	}
	return CallSpec{}, false
}

// Registry 保存全部已注册动作，并在注册期做声明校验。
type Registry struct {
	actions map[string]*Action
}

func NewRegistry() *Registry { return &Registry{actions: map[string]*Action{}} }

// Register 注册动作，并在注册期（调用链条确定时）完成声明校验：
// 任何对非关键调用输出的消费依赖都直接判定为声明错误。
func (r *Registry) Register(a *Action) error {
	if a.Name == "" {
		return fmt.Errorf("ontology: action with empty name")
	}
	if a.Run == nil {
		return fmt.Errorf("ontology: action %q has no Run function", a.Name)
	}
	if _, dup := r.actions[a.Name]; dup {
		return fmt.Errorf("ontology: action %q registered twice", a.Name)
	}
	for _, ref := range a.Consumes {
		spec, ok := a.callSpec(ref.Call)
		if !ok {
			return fmt.Errorf("ontology: action %q consumes output %q of undeclared call %q",
				a.Name, ref.Output, ref.Call)
		}
		if !spec.Critical {
			return fmt.Errorf("ontology: action %q consumes output %q of non-critical call %q: "+
				"a non-critical call may fail and its outputs must never be relied upon",
				a.Name, ref.Output, ref.Call)
		}
	}
	r.actions[a.Name] = a
	return nil
}

func (r *Registry) Get(name string) (*Action, bool) {
	a, ok := r.actions[name]
	return a, ok
}

// Validate 校验所有静态声明的嵌套调用目标均已注册。
func (r *Registry) Validate() error {
	for _, a := range r.actions {
		for _, c := range a.Calls {
			if _, ok := r.actions[c.Action]; !ok {
				return fmt.Errorf("ontology: action %q declares call to unregistered action %q",
					a.Name, c.Action)
			}
		}
	}
	return nil
}
