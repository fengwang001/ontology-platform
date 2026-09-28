package ontology

// Event 是按键分区、按序号排序的变更事件。
type Event struct {
	Key   string
	Seq   int64
	Value string
}

// Applier 把单个事件应用到底层存储。实现必须无副作用重试安全。
type Applier interface {
	Apply(ev Event) error
}

// ApplierFunc 便于用函数实现 Applier。
type ApplierFunc func(ev Event) error

func (f ApplierFunc) Apply(ev Event) error { return f(ev) }
