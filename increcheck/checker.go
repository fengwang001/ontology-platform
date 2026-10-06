package increcheck

import "sort"

// checker.go：声明级检查器。
//
// 语言模型（刻意保持简单、确定、无外部依赖，便于与朴素模型对照）：
//   - 签名文本是一段文本，其中以 "$" 开头、后接字母数字的记号是对其它
//     声明签名的引用，形如 "f($x,$y)"。
//   - 检查签名 = 逐个读取被引用声明的当前签名；任一被引用者不存在，
//     该签名结果为「未定义」错误态（错误本身也是可依据的签名结果）。
//   - 实现文本同理；检查实现额外读取自身签名。
// 读取发生在检查期间即被动记入 sink，调用顺序无关：最终依赖集合相同。

// SigResult 是一次签名检查的确定结果（错误态也是结果）。
type SigResult struct {
	Present bool
	Text    string // 规范化后的签名文本；不存在时为空
	Err     ErrCode
	// PoisonedBy 记录结果传递自哪个错误态签名（即使自身文本可解析、
	// 自身引用都存在，也因读取错误态签名而带毒）。
	// 带毒结果与正常结果身份不同：依赖错误组的下游按签名已变化处理。
	PoisonedBy string
}

// equal 是「早停」唯一依据：重检后的实际结果与旧结果逐字段比较。
func (r SigResult) equal(o SigResult) bool {
	return r == o
}

// ImplResult 是一次实现检查的确定结果。
type ImplResult struct {
	Err ErrCode
}

// Checker 依据登记内容执行检查。Calls 统计实际检查次数，
// 供复杂度证明测试断言（仅在测试场景注入，生产路径为 nil）。
type Checker struct {
	Calls int
}

func NewChecker() *Checker { return &Checker{} }

// SigReader 返回某标识当前已知的签名结果；读取即被动记为依赖。
// 不存在标识返回 Present=false。
type SigReader func(id string) SigResult

// CheckSig 检查 id 的签名：每读取一个引用签名就记为依赖。
// 读取到不存在或错误态的签名都使结果为未定义错误；读取错误态时
// PoisonedBy 指向错误来源，使错误沿签名依赖在结果身份上持续可见。
func (c *Checker) CheckSig(reg *Registry, id string, read SigReader) SigResult {
	c.Calls++
	d, ok := reg.get(id)
	if !ok {
		return SigResult{Present: false}
	}
	refs := parseRefs(d.SigText)
	firstErr := ""
	for _, ref := range refs {
		// 即使首个引用已决定错误，也要继续读取并记录其余引用：
		// 依赖是「本次检查读取的全部签名」，不能因提前报错而漏记，
		// 否则被漏掉的依赖将来恢复时无法使本检查失效。
		r := read(ref)
		if firstErr == "" && (!r.Present || r.Err != ErrNone) {
			if !r.Present {
				firstErr = ref + ":absent"
			} else {
				firstErr = ref
			}
		}
	}
	if firstErr != "" {
		poison := firstErr
		if n := len(poison); n > 7 && poison[n-7:] == ":absent" {
			poison = poison[:n-7]
		}
		return SigResult{Present: true, Text: d.SigText, Err: ErrUndefined, PoisonedBy: poison}
	}
	return SigResult{Present: true, Text: d.SigText}
}

// CheckImpl 检查 id 的实现：读取自身签名，再读取实现体引用的签名。
func (c *Checker) CheckImpl(reg *Registry, id string, read SigReader) ImplResult {
	c.Calls++
	d, ok := reg.get(id)
	if !ok {
		return ImplResult{Err: ErrUndefined}
	}
	if r := read(id); !r.Present || r.Err != ErrNone {
		// 仍继续记录实现体中的全部引用，保证实现依赖边完整。
		bad := true
		for _, ref := range parseRefs(d.ImplText) {
			if ref != id {
				read(ref)
			}
		}
		_ = bad
		return ImplResult{Err: ErrUndefined}
	}
	bad := false
	for _, ref := range parseRefs(d.ImplText) {
		if ref == id {
			continue
		}
		r := read(ref)
		if !r.Present || r.Err != ErrNone {
			bad = true
		}
	}
	if bad {
		return ImplResult{Err: ErrUndefined}
	}
	return ImplResult{}
}

// parseRefs 提取文本中所有 "$ident" 形式的引用，去重后按字典序返回，
// 保证任何检查的依赖集合有客观唯一的表示。
func parseRefs(text string) []string {
	seen := map[string]struct{}{}
	for i := 0; i < len(text); i++ {
		if text[i] != '$' {
			continue
		}
		j := i + 1
		for j < len(text) && isIdent(text[j]) {
			j++
		}
		if j > i+1 {
			seen[text[i+1:j]] = struct{}{}
		}
		i = j - 1
	}
	refs := make([]string, 0, len(seen))
	for r := range seen {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs
}

func isIdent(b byte) bool {
	return b == '_' ||
		('a' <= b && b <= 'z') ||
		('A' <= b && b <= 'Z') ||
		('0' <= b && b <= '9')
}
