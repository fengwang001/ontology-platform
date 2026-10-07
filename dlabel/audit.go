package dlabel

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// TagVerdict 记录某次裁决中单个标签的判定依据。
type TagVerdict struct {
	Tag     string
	Carried bool
}

// AuditEntry 完整记录一次调用的输入、最终输出与裁决依据。
type AuditEntry struct {
	Seq        int64
	Time       time.Time
	Call       string
	Subject    string
	ObjectType string
	Instance   string
	Inputs     string
	Outputs    string
	ErrCode    ErrorCode
	Err        string
	AttrsRead  []string
	Basis      []TagVerdict
}

// AuditLog 是审计日志接口。
type AuditLog interface {
	Record(e AuditEntry)
}

// MemoryAuditLog 是默认的内存审计日志，保留最近 capacity 条（0 表示不限）。
// 它只追加、不参与任何权限裁决，被拒绝的调用同样被完整记录。
type MemoryAuditLog struct {
	mu       sync.Mutex
	entries  []AuditEntry
	capacity int
}

func NewMemoryAuditLog(capacity int) *MemoryAuditLog {
	return &MemoryAuditLog{capacity: capacity}
}

func (l *MemoryAuditLog) Record(e AuditEntry) {
	l.mu.Lock()
	l.entries = append(l.entries, e)
	if l.capacity > 0 && len(l.entries) > l.capacity {
		l.entries = l.entries[len(l.entries)-l.capacity:]
	}
	l.mu.Unlock()
}

// Entries 返回日志条目的快照副本。
func (l *MemoryAuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	out := make([]AuditEntry, len(l.entries))
	copy(out, l.entries)
	l.mu.Unlock()
	return out
}

func (p *Platform) beginAudit(call, subject, ot, id, inputs string) AuditEntry {
	return AuditEntry{
		Seq:        p.seq.Add(1),
		Time:       time.Now(),
		Call:       call,
		Subject:    subject,
		ObjectType: ot,
		Instance:   id,
		Inputs:     inputs,
	}
}

func (p *Platform) finishAudit(e AuditEntry, out any, err error) {
	if err != nil {
		e.ErrCode = errorCode(err)
		e.Err = err.Error()
	} else if out != nil {
		e.Outputs = formatOutput(out)
		if r, ok := out.(*ReadResult); ok {
			e.Outputs = formatReadResult(r)
		}
		if x, ok := out.(*Explanation); ok {
			e.Outputs = formatExplanation(x)
		}
	}
	if len(e.AttrsRead) == 0 {
		e.AttrsRead = nil
	}
	p.audit.Record(e)
}

func (p *Platform) finishAuditFull(e AuditEntry, out any, err error, attrsRead []string, basis []TagVerdict) {
	if len(attrsRead) > 0 {
		e.AttrsRead = append([]string(nil), attrsRead...)
	}
	if len(basis) > 0 {
		e.Basis = append([]TagVerdict(nil), basis...)
	}
	p.finishAudit(e, out, err)
}

func formatValue(m map[string]Value) string {
	if len(m) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(m))
	for _, k := range sortedStringKeys(m) {
		parts = append(parts, fmt.Sprintf("%s=%s", k, formatOneValue(m[k])))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func sortedStringKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func formatOneValue(v Value) string {
	switch v.kind {
	case KindNull:
		return "null"
	case KindInt:
		return fmt.Sprintf("%d", v.i)
	case KindFloat:
		return fmt.Sprintf("%g", v.f)
	case KindString:
		return fmt.Sprintf("%q", v.s)
	case KindBool:
		if v.b {
			return "true"
		}
		return "false"
	default:
		return "?"
	}
}

func formatValueMap(m map[string]Value) string { return formatValue(m) }
func formatAttrKinds(m map[string]ValueKind) string {
	parts := make([]string, 0, len(m))
	for _, k := range sortedStringKeys(m) {
		parts = append(parts, fmt.Sprintf("%s:%d", k, m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatRules(rules []Rule) string {
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		parts = append(parts, r.ObjectType+":"+r.Tag)
	}
	return strings.Join(parts, ", ")
}

func formatGrant(g Grant) string {
	return fmt.Sprintf("subject=%s tag=%s read=%d write=%d vis=%d attrs=[%s]",
		g.Subject, g.Tag, g.Read, g.Write, g.Visibility, strings.Join(g.Attrs, ","))
}

func joinStrings(ss []string) string { return strings.Join(ss, ",") }

func formatOutput(out any) string { return fmt.Sprintf("%v", out) }

func formatReadResult(r *ReadResult) string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, len(r.Attrs))
	for _, a := range sortedStringKeys(r.Attrs) {
		ar := r.Attrs[a]
		if !ar.Allowed {
			parts = append(parts, a+"=DENIED")
		} else {
			parts = append(parts, a+"="+formatOneValue(ar.Value))
		}
	}
	return fmt.Sprintf("v%d {%s}", r.Version, strings.Join(parts, ", "))
}

func formatExplanation(x *Explanation) string {
	if x == nil {
		return ""
	}
	basis := make([]string, len(x.Basis))
	for i, b := range x.Basis {
		basis[i] = fmt.Sprintf("%s=%t", b.Tag, b.Carried)
	}
	return fmt.Sprintf("%s attrsRead=[%s] basis={%s}",
		formatReadResult(x.Result), strings.Join(x.AttrsRead, ","), strings.Join(basis, ","))
}
