package ontology

import (
	"fmt"
	"regexp"
	"strings"
)

// segmentRe 限定字面段只允许字母、数字、下划线与中划线。
var segmentRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// pattern 是编译后的单层匹配模式。段为字面名，或仅允许出现在末段的 "*"。
type pattern struct {
	raw  string
	segs []string
}

// parsePattern 校验并解析一条模式。拒绝：空模式、空段、多层通配（如 a.*.b）、
// 段内嵌通配（如 ab*）以及非法字符。
func parsePattern(raw string) (pattern, error) {
	if raw == "" {
		return pattern{}, fmt.Errorf("pattern is empty")
	}
	segs := strings.Split(raw, ".")
	for i, seg := range segs {
		if seg == "" {
			return pattern{}, fmt.Errorf("empty segment")
		}
		if seg == "*" {
			if i != len(segs)-1 {
				return pattern{}, fmt.Errorf("wildcard \"*\" is only allowed as the final segment")
			}
			continue
		}
		if strings.Contains(seg, "*") {
			return pattern{}, fmt.Errorf("wildcard \"*\" must occupy a whole segment")
		}
		if !segmentRe.MatchString(seg) {
			return pattern{}, fmt.Errorf("illegal characters in segment %q", seg)
		}
	}
	return pattern{raw: raw, segs: segs}, nil
}

// matches 仅当段数相同且每段匹配（字面相等或末段通配）时成立，因此单层
// 通配不会跨层匹配更深的路径。
func (p pattern) matches(path []string) bool {
	if len(p.segs) != len(path) {
		return false
	}
	for i, seg := range p.segs {
		if seg != "*" && seg != path[i] {
			return false
		}
	}
	return true
}

// specificity 返回（字面段数, 总段数），字面段越多越具体。
func (p pattern) specificity() (literals, segments int) {
	for _, seg := range p.segs {
		if seg != "*" {
			literals++
		}
	}
	return literals, len(p.segs)
}

// moreSpecific 报告 a 是否严格比 b 具体。
func moreSpecific(a, b pattern) bool {
	al, as := a.specificity()
	bl, bs := b.specificity()
	if al != bl {
		return al > bl
	}
	return as > bs
}

// sameSpecificity 报告两条模式具体度是否相同。
func sameSpecificity(a, b pattern) bool {
	al, as := a.specificity()
	bl, bs := b.specificity()
	return al == bl && as == bs
}
