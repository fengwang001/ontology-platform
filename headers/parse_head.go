package headers

import (
	"strings"

	"ontology/fold"
	"ontology/policy"
	"ontology/token"
)

func splitHead(line string, idx, lineNo int) (string, string, error) {
	i := 0
	for i < len(line) && token.IsTChar(line[i]) {
		i++
	}
	if i == 0 {
		return "", "", &ParseError{Stage: StageName, Index: idx, Line: lineNo,
			Err: token.ErrIllegalName, Msg: "empty or illegal header name"}
	}
	if i >= len(line) {
		return "", "", &ParseError{Stage: StageColon, Index: idx, Line: lineNo,
			Msg: "missing colon (truncated?)"}
	}
	if line[i] == ' ' || line[i] == '\t' {
		return "", "", &ParseError{Stage: StageColon, Index: idx, Line: lineNo,
			Err: ErrNameColonSpace, Msg: "whitespace between name and colon"}
	}
	if line[i] != ':' {
		return "", "", &ParseError{Stage: StageName, Index: idx, Line: lineNo,
			Err: token.ErrIllegalName, Msg: "illegal byte in header name"}
	}
	return line[:i], line[i+1:], nil
}

// commit 把一个头部（名字 + 冒号后余串 + 续行）展开、校验、规范化后入构建器。
func (b *builder) commit(rawName, afterColon string, conts []string, lineNo int) error {
	cfg := b.set.cfg
	if len(rawName) > cfg.MaxName {
		return &ParseError{Stage: StageName, Index: b.count(), Line: lineNo,
			Err: ErrNameTooLong, Msg: "name exceeds limit"}
	}
	p := b.set.reg.For(rawName)
	canon := policy.Canonical(rawName, p)
	if canon != rawName {
		b.norm = true
	}
	value := fold.Unfold(afterColon, conts)
	trimmed := strings.Trim(value, " \t")
	if afterColon != " "+trimmed || len(conts) > 0 {
		b.norm = true // 首尾空白、折行都会被规范化
	}
	if len(trimmed) > cfg.MaxValue {
		return &ParseError{Stage: StageValue, Index: b.count(), Line: lineNo,
			Err: ErrValueTooLong, Msg: "value exceeds limit"}
	}
	if err := policy.ValidateValue(p, trimmed); err != nil {
		return &ParseError{Stage: StageValue, Index: b.count(), Line: lineNo,
			Err: err, Msg: "illegal or unsafe value"}
	}
	if b.count()+1 > cfg.MaxHeaders {
		return &ParseError{Stage: StageName, Index: b.count(), Line: lineNo,
			Err: ErrTooManyHeaders, Msg: "header count exceeds limit"}
	}
	b.ents = append(b.ents, entry{name: canon, value: trimmed})
	return nil
}
