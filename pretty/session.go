package pretty

import "sync"

// OverlongLine 描述一个超宽行：去掉行尾空格后宽度仍超过行宽。
// 超宽不是错误，超宽行的内容照常保留在渲染结果中。
type OverlongLine struct {
	Line  int // 行号，从 1 开始
	Width int // 去掉行尾空格后的宽度
}

// Result 是渲染结果。
type Result struct {
	Text     string         // 渲染出的文本
	Overlong []OverlongLine // 超宽行清单，按行号升序
}

// Session 持有一个命名片段登记表，供文档引用。Session 可并发使用：
// 登记与渲染可同时进行，效果等价于所有已接受操作按某个串行顺序执行；
// 每次渲染看到的是登记表的一个一致快照。
type Session struct {
	mu    sync.RWMutex
	frags map[string]Doc
}

// NewSession 返回一个空的会话。
func NewSession() *Session {
	return &Session{frags: make(map[string]Doc)}
}

// Register 登记命名片段。片段名必须非空且不重复；片段只能引用已登记
// 的片段，因此引用关系不可能成环。登记被拒绝时会话状态不变。
func (s *Session) Register(name string, d Doc) error {
	if name == "" {
		return &Error{Code: ErrInvalidParam, Detail: "片段名为空"}
	}
	if d == nil {
		return &Error{Code: ErrInvalidParam, Detail: "片段文档为 nil"}
	}
	msg, refs := checkParams(asNode(d))
	if msg != "" {
		return &Error{Code: ErrInvalidParam, Detail: msg}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.frags[name]; dup {
		return &Error{Code: ErrDuplicateName, Detail: name}
	}
	for _, r := range refs {
		if _, ok := s.frags[r]; !ok {
			return &Error{Code: ErrUnregisteredRef, Detail: r}
		}
	}
	s.frags[name] = d
	return nil
}

// snapshot 返回登记表的一致快照（浅拷贝；Doc 不可变，故快照稳定）。
func (s *Session) snapshot() map[string]Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]Doc, len(s.frags))
	for k, v := range s.frags {
		cp[k] = v
	}
	return cp
}

// Render 按给定行宽渲染文档。渲染对已构造的文档树与已登记的片段是
// 只读的，不改变会话状态。
func (s *Session) Render(d Doc, width int) (Result, error) {
	if width < MinWidth || width > MaxWidth {
		return Result{}, &Error{Code: ErrInvalidParam, Detail: "行宽超出 [1, 10000]"}
	}
	if d == nil {
		return Result{}, &Error{Code: ErrInvalidParam, Detail: "文档为 nil"}
	}
	frags := s.snapshot()
	root := asNode(d)
	info, err := analyze(root, frags)
	if err != nil {
		return Result{}, err
	}
	res, rerr := render(root, width, info, frags)
	if rerr != nil {
		return Result{}, rerr
	}
	return res, nil
}

// Render 是使用全新空会话渲染的便捷形式。
func Render(d Doc, width int) (Result, error) {
	return NewSession().Render(d, width)
}
