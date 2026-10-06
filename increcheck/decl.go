package increcheck

// decl.go：声明与引用登记。
// 维护声明源码（签名文本 / 实现文本）、签名结果的版本号，
// 以及「不存在」声明的墓碑版本：删除即签名变为「不存在」，
// 墓碑仍占一个版本，重新添加再递增，从而触发依赖者二次失效。

// ErrCode 是编辑请求与检查的错误类别。
type ErrCode int

const (
	ErrNone         ErrCode = iota
	ErrDeclNotFound         // 编辑目标声明标识不存在
	ErrDeclExists           // 添加的标识已存在
	ErrNoOp                 // 编辑内容与现有内容完全相同
	ErrBusy                 // 调度进行中提交编辑，被拒绝
	ErrUndefined            // 检查时引用了不存在的声明
)

func (c ErrCode) Error() string {
	switch c {
	case ErrDeclNotFound:
		return "declaration not found"
	case ErrDeclExists:
		return "declaration already exists"
	case ErrNoOp:
		return "edit is a no-op"
	case ErrBusy:
		return "scheduler busy, edit rejected"
	case ErrUndefined:
		return "reference to undefined declaration"
	default:
		return "ok"
	}
}

// EditError 携带错误类别，便于调用方按类别断言。
type EditError struct{ Code ErrCode }

func (e *EditError) Error() string { return e.Code.Error() }

// Declaration 是一条声明的可编辑源码。
type Declaration struct {
	SigText  string
	ImplText string
}

// Registry 登记全部声明源码与签名版本。
// 版本号在登记层单调递增；调用方（调度器）只在签名结果
// 实际发生变化时才调用 bump，因此「重检后相同不递增」。
type Registry struct {
	source  map[string]Declaration
	version map[string]int64
}

func NewRegistry() *Registry {
	return &Registry{
		source:  map[string]Declaration{},
		version: map[string]int64{},
	}
}

// get 返回声明源码；不存在时 ok=false（签名即「不存在」）。
func (r *Registry) get(id string) (Declaration, bool) {
	d, ok := r.source[id]
	return d, ok
}

// sigVersion 返回签名当前版本；从未出现过的标识版本为 0。
func (r *Registry) sigVersion(id string) int64 {
	return r.version[id]
}

// list 按字典序返回全部现存标识。
func (r *Registry) list() []string {
	ids := make([]string, 0, len(r.source))
	for id := range r.source {
		ids = append(ids, id)
	}
	return sortedStrings(ids)
}

func (r *Registry) bump(id string) int64 {
	r.version[id]++
	return r.version[id]
}

// add 登记一条新声明；标识已存在（含墓碑被复用的情形按现存判定）
// 时返回 ErrDeclExists。添加本身视为签名从「不存在」变为存在，
// 版本递增由调度器在判定结果确实变化后显式完成。
func (r *Registry) add(id string, d Declaration) error {
	if _, ok := r.source[id]; ok {
		return &EditError{ErrDeclExists}
	}
	r.source[id] = d
	return nil
}

// editSig 只改签名文本；内容完全相同返回 ErrNoOp，不改任何状态。
func (r *Registry) editSig(id, sig string) error {
	d, ok := r.source[id]
	if !ok {
		return &EditError{ErrDeclNotFound}
	}
	if d.SigText == sig {
		return &EditError{ErrNoOp}
	}
	d.SigText = sig
	r.source[id] = d
	return nil
}

// editImpl 只改实现文本；内容完全相同返回 ErrNoOp。
func (r *Registry) editImpl(id, impl string) error {
	d, ok := r.source[id]
	if !ok {
		return &EditError{ErrDeclNotFound}
	}
	if d.ImplText == impl {
		return &EditError{ErrNoOp}
	}
	d.ImplText = impl
	r.source[id] = d
	return nil
}

// delete 删除声明源码，保留并递增其墓碑版本。
// delete 删除声明源码；墓碑版本的递增由调度器在采纳「不存在」结果时完成。
func (r *Registry) delete(id string) error {
	if _, ok := r.source[id]; !ok {
		return &EditError{ErrDeclNotFound}
	}
	delete(r.source, id)
	return nil
}
