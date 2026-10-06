package suppress

import (
	"sort"
	"strings"
	"sync"
)

// Session 是线程安全的登记会话：多个调用方可并发登记诊断与指令，
// 判定（Evaluate）可与登记并发执行，看到的是某个已接受操作序列的一致快照。
type Session struct {
	mu            sync.RWMutex
	totalLines    int
	requireReason bool
	rules         map[string]struct{}
	diagnostics   []Diagnostic
	directives    []acceptedDirective
	duplicates    map[string]struct{}
}

// NewSession 创建会话。totalLines 为源文件总行数（须 >=1）；
// rules 为已知规则名集合；requireReason 为 true 时空白理由使整条指令失效。
func NewSession(totalLines int, rules []string, requireReason bool) *Session {
	ruleSet := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		ruleSet[r] = struct{}{}
	}
	return &Session{
		totalLines:    totalLines,
		requireReason: requireReason,
		rules:         ruleSet,
		duplicates:    make(map[string]struct{}),
	}
}

// AddDiagnostic 登记一条诊断。参数非法时返回对应错误且不改变会话状态。
func (s *Session) AddDiagnostic(d Diagnostic) *RegError {
	if d.Line < 1 || d.Line > s.totalLines {
		return &RegError{Code: ErrInvalidParameter, Detail: "diagnostic line out of range [1, totalLines]"}
	}
	if d.Column < 1 {
		return &RegError{Code: ErrInvalidParameter, Detail: "diagnostic column must be >= 1"}
	}
	if d.Rule == "" {
		return &RegError{Code: ErrInvalidParameter, Detail: "diagnostic rule must not be empty"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diagnostics = append(s.diagnostics, d)
	return nil
}

// AddDirective 登记一条指令。非法参数或完全重复的登记会被拒绝且不改变会话状态。
func (s *Session) AddDirective(d Directive) *RegError {
	// 参数非法优先于重复判定。
	if d.Line < 1 || d.Line > s.totalLines {
		return &RegError{Code: ErrInvalidParameter, Detail: "directive line out of range [1, totalLines]"}
	}
	if d.Kind < KindThisLine || d.Kind > KindWholeFile {
		return &RegError{Code: ErrInvalidParameter, Detail: "unknown directive kind"}
	}
	key := directiveKey(d)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.duplicates[key]; ok {
		return &RegError{Code: ErrDuplicate, Detail: "identical directive already registered"}
	}
	s.duplicates[key] = struct{}{}
	s.directives = append(s.directives, acceptedDirective{
		Directive: Directive{Line: d.Line, Kind: d.Kind, Labels: normalizeLabels(d.Labels), Reason: d.Reason},
		order:     len(s.directives),
	})
	return nil
}

// normalizeLabels 复制标签、按首次出现去重；空列表规范化为只含“全部”。
func normalizeLabels(labels []string) []string {
	if len(labels) == 0 {
		return []string{AllTag}
	}
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	return out
}

// directiveKey 构造重复判定键：行号、种类、标签集合（与次序无关）、理由。
func directiveKey(d Directive) string {
	set := normalizeLabels(d.Labels)
	sort.Strings(set)
	return strings.Join([]string{
		itoa(d.Line),
		d.Kind.String(),
		strings.Join(set, "\x00"),
		d.Reason,
	}, "\x01")
}

// itoa 是免导入 strconv 的轻量整数格式化（键只用于进程内比较）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	var buf [24]byte
	i := len(buf)
	for n != 0 {
		i--
		dig := n % 10
		if dig < 0 {
			dig = -dig
		}
		buf[i] = byte('0' + dig)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// snapshot 是某个时刻已接受登记的不可变一致快照。
type snapshot struct {
	totalLines    int
	requireReason bool
	rules         map[string]struct{}
	diagnostics   []Diagnostic
	directives    []acceptedDirective
}

// acceptedDirective 是登记后（标签已规范化去重、带登记次序）的指令。
type acceptedDirective struct {
	Directive
	order int
}

// takeSnapshot 在一致视图上复制当前已接受的登记内容。
func (s *Session) takeSnapshot() snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	diagnostics := append([]Diagnostic(nil), s.diagnostics...)
	directives := make([]acceptedDirective, len(s.directives))
	for i, d := range s.directives {
		labels := append([]string(nil), d.Labels...)
		directives[i] = acceptedDirective{
			Directive: Directive{Line: d.Line, Kind: d.Kind, Labels: labels, Reason: d.Reason},
			order:     d.order,
		}
	}
	return snapshot{
		totalLines:    s.totalLines,
		requireReason: s.requireReason,
		rules:         s.rules,
		diagnostics:   diagnostics,
		directives:    directives,
	}
}

// Evaluate 在一致快照上执行判定，可重复调用且结果一致。
func (s *Session) Evaluate() Judgment {
	return evaluate(s.takeSnapshot())
}
