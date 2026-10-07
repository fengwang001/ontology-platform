package ontology

import "context"

// funcImpl is a function-backed implementation used across tests.
type funcImpl struct {
	name string
	pre  func(any) bool
	run  func(*ExecContext, any) (any, error)
	post func(any, any) bool
}

func (f *funcImpl) Pre(in any) bool {
	if f.pre != nil {
		return f.pre(in)
	}
	return true
}

func (f *funcImpl) Execute(ctx *ExecContext, in any) (any, error) {
	if f.run != nil {
		return f.run(ctx, in)
	}
	return f.name, nil
}

func (f *funcImpl) Post(in, out any) bool {
	if f.post != nil {
		return f.post(in, out)
	}
	return true
}

func mkImpl(name string) *funcImpl {
	return &funcImpl{name: name}
}

// hierarchy builds concrete -> p1 -> p2 -> p3 -> root and returns all nodes
// in the order (root, p3, p2, p1, concrete).
func hierarchy() (root, p3, p2, p1, concrete *ObjectType) {
	root = &ObjectType{ID: "root"}
	p3 = &ObjectType{ID: "p3", Parent: root}
	p2 = &ObjectType{ID: "p2", Parent: p3}
	p1 = &ObjectType{ID: "p1", Parent: p2}
	concrete = &ObjectType{ID: "concrete", Parent: p1}
	return
}

func mkAction(id ActionID) *Action {
	return &Action{
		ID:            id,
		InputType:     "any",
		OutputType:    "string",
		Precondition:  func(any) bool { return true },
		Postcondition: func(_, _ any) bool { return true },
	}
}

func newSetup() (*Registry, *Dispatcher, *Action) {
	reg := NewRegistry()
	d := NewDispatcher(reg)
	a := mkAction("do")
	reg.DeclareAction(a)
	return reg, d, a
}

func invokeOK(d *Dispatcher, obj *Instance, a ActionID, in any) (any, *DispatchTrace) {
	out, tr, err := d.Invoke(context.Background(), obj, a, in)
	if err != nil {
		panic("unexpected invoke error: " + err.Error())
	}
	return out, tr
}
