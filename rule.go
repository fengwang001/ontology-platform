package ontology

import (
	"errors"
	"fmt"
	"strings"
)

// Action 为规则动作。
type Action int

const (
	Include Action = iota + 1
	Exclude
)

// Rule 是一条有序的包含/排除规则。
type Rule struct {
	Action  Action
	Pattern string
}

// patternKind 描述模式形态。
type patternKind int

const (
	kindExact patternKind = iota + 1
	kindPrefix
	kindChildren
)

// compiled 是校验并编译后的规则集：
// 按「作用目录」建立索引，使单路径判定只访问其祖先链。
type compiled struct {
	order int
	// exact[dir][name] = 作用于 dir/name 的最后一条精确规则
	exact map[string]map[string]indexedRule
	// child[dir][name] = 作用于 dir 下直接子项 name 的通配段规则
	child map[string]map[string]indexedRule
	// prefix[dir] = 以 dir/ 结尾的前缀规则，键为目录路径
	prefix map[string]indexedRule
	// trie 是按目录组织的规则树（含子树指纹与包含标记），惰性填充。
	trie *ruleTrie
}

type indexedRule struct {
	index  int
	action Action
}

// parsedPattern 是模式解析结果。
type parsedPattern struct {
	kind   patternKind
	dir    string // 作用目录（前缀模式为其自身），根为 ""
	segs   []string
	target string // 精确/通配段命中的子项名
}

func parsePattern(p string) (parsedPattern, error) {
	if p == "" {
		return parsedPattern{}, errors.New("empty pattern")
	}
	if strings.Contains(p, "//") {
		return parsedPattern{}, fmt.Errorf("consecutive separator in pattern %q", p)
	}
	if strings.HasSuffix(p, "/") {
		dir := strings.TrimSuffix(p, "/")
		if strings.HasPrefix(dir, "/") {
			return parsedPattern{}, fmt.Errorf("leading separator in pattern %q", p)
		}
		segs := splitPath(dir)
		for _, s := range segs {
			if s == "" || s == ".." {
				return parsedPattern{}, fmt.Errorf("illegal segment in pattern %q", p)
			}
		}
		return parsedPattern{kind: kindPrefix, dir: dir, segs: segs}, nil
	}
	segs := splitPath(p)
	if strings.HasPrefix(p, "/") {
		return parsedPattern{}, fmt.Errorf("leading separator in pattern %q", p)
	}
	for _, s := range segs {
		if s == "" || s == ".." {
			return parsedPattern{}, fmt.Errorf("illegal segment in pattern %q", p)
		}
		if strings.Contains(s, "*") && s != "*" {
			return parsedPattern{}, fmt.Errorf("wildcard must be a whole segment in %q", p)
		}
	}
	if segs[len(segs)-1] == "*" {
		dirSegs := segs[:len(segs)-1]
		return parsedPattern{
			kind:   kindChildren,
			dir:    joinSegs(dirSegs),
			segs:   dirSegs,
			target: "*",
		}, nil
	}
	return parsedPattern{
		kind:   kindExact,
		dir:    joinSegs(segs[:len(segs)-1]),
		segs:   segs[:len(segs)-1],
		target: segs[len(segs)-1],
	}, nil
}

// validateAndCompile 校验规则集参数并编译；非法返回带原因的错误。
func validateAndCompile(rules []Rule) (*compiled, error) {
	c := &compiled{
		exact:  map[string]map[string]indexedRule{},
		child:  map[string]map[string]indexedRule{},
		prefix: map[string]indexedRule{},
	}
	for i, r := range rules {
		if r.Action != Include && r.Action != Exclude {
			return nil, fmt.Errorf("rule %d: unknown action", i)
		}
		pp, err := parsePattern(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("rule %d (%q): %w", i, r.Pattern, err)
		}
		c.order = i + 1
		switch pp.kind {
		case kindExact:
			m := c.exact[pp.dir]
			if m == nil {
				m = map[string]indexedRule{}
				c.exact[pp.dir] = m
			}
			if prev, ok := m[pp.target]; ok && prev.action == r.Action {
				return nil, fmt.Errorf("rule %d: meaningless duplicate of rule %d on %q", i, prev.index, r.Pattern)
			}
			m[pp.target] = indexedRule{index: i, action: r.Action}
		case kindChildren:
			m := c.child[pp.dir]
			if m == nil {
				m = map[string]indexedRule{}
				c.child[pp.dir] = m
			}
			if prev, ok := m["*"]; ok && prev.action == r.Action {
				return nil, fmt.Errorf("rule %d: meaningless duplicate of rule %d on %q", i, prev.index, r.Pattern)
			}
			m["*"] = indexedRule{index: i, action: r.Action}
		case kindPrefix:
			if prev, ok := c.prefix[pp.dir]; ok && prev.action == r.Action {
				return nil, fmt.Errorf("rule %d: meaningless duplicate of rule %d on %q", i, prev.index, r.Pattern)
			}
			c.prefix[pp.dir] = indexedRule{index: i, action: r.Action}
		}
	}
	return c, nil
}

// dirKey 是规则作用目录的内部键；根目录为空串。
func dirKey(segs []string) string { return joinSegs(segs) }
