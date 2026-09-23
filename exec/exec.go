package exec

import "ontology/deque"

// Executor 是固定 N worker 的 work-stealing 执行器。
type Executor struct {
	deques []*deque.Deque[task]
}

type task struct{}
type taskFn func(ctx *Context)

// Context 传给任务函数，允许执行中再 Spawn。
type Context struct{}

func (c *Context) Spawn(g interface{}, fn taskFn) error { return nil }

func New(n int) *Executor { return &Executor{} }

func (e *Executor) RootGroup() interface{}        { return nil }
func (e *Executor) Spawn(g interface{}, fn taskFn) error { return nil }
func (e *Executor) Wait()                         {}
func (e *Executor) Close()                        {}
