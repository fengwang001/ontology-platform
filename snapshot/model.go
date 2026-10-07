// Package snapshot 实现本体平台的快照导出协同组件。
//
// 组件在持续有新写入到达的情况下，产出一份对应明确逻辑边界位置的全量
// 快照，以及紧随其后的增量记录流，使下游消费方能够将两者无缝拼接，
// 还原出任意时点上系统完整且自洽的状态。
package snapshot

// LSN 是写入被接受时由日志分配的全局唯一、单调递增的逻辑序号。
// 它同时充当：写入接受顺序的全序、快照边界位置的表示、以及增量
// 归属判定的唯一依据。
type LSN uint64

// TxnID 标识一笔事务（一次动作执行）。一笔事务内的全部修改共享
// 同一个 TxnID，并作为一个整体对外可见。
type TxnID string

// WriteKind 区分三类本体数据写入。
type WriteKind int

const (
	// KindObjectUpsert 对象类型实例的新建或更新。
	KindObjectUpsert WriteKind = iota
	// KindLinkUpsert 链接实例的新建或更新。
	KindLinkUpsert
	// KindActionRecord 动作执行记录。
	KindActionRecord
)

// Object 是一条对象类型实例记录。
type Object struct {
	ID    string
	Type  string
	Props map[string]string
}

// Link 是一条链接实例记录，两端引用对象实例。
type Link struct {
	ID   string
	Type string
	Src  string
	Dst  string
}

// ActionRecord 是一条动作执行记录。
type ActionRecord struct {
	ID     string
	Action string
}

// Write 是日志中一条已被接受的写入。
type Write struct {
	LSN    LSN
	Txn    TxnID
	Kind   WriteKind
	Object *Object
	Link   *Link
	Action *ActionRecord
}

// WriteSpec 是写入请求（尚未分配 LSN）。
type WriteSpec struct {
	Txn    TxnID
	Kind   WriteKind
	Object *Object
	Link   *Link
	Action *ActionRecord
}

// State 是某一逻辑边界上的物化状态：每条记录取边界之前最后一次
// 写入的最终结果。
type State struct {
	Objects map[string]Object
	Links   map[string]Link
	Actions map[string]ActionRecord
}

// NewState 返回一个空状态。
func NewState() State {
	return State{
		Objects: map[string]Object{},
		Links:   map[string]Link{},
		Actions: map[string]ActionRecord{},
	}
}

// Apply 将一条写入物化到状态中，供下游拼接快照与增量使用。
func (s State) Apply(w Write) {
	switch w.Kind {
	case KindObjectUpsert:
		s.Objects[w.Object.ID] = *w.Object
	case KindLinkUpsert:
		s.Links[w.Link.ID] = *w.Link
	case KindActionRecord:
		s.Actions[w.Action.ID] = *w.Action
	}
}

// Increment 是一条增量记录。为保证任意前缀拼接后事务整体可见或
// 整体不可见，一条增量记录恰好承载一笔完整事务的全部写入。
type Increment struct {
	Txn     TxnID
	FromLSN LSN
	ToLSN   LSN
	Writes  []Write
}
