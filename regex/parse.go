package regex

// 模式语法（按字节处理）：
//   字面字节；'.' 匹配除 '\n' 外任意字节；字符集 [abc] [a-c] [^a]（集合内无转义，
//   范围要求 lo<=hi）；'\' 加任一字节表示该字节字面；'|' 选择；'( )' 不捕获分组；
//   '^' '$' 断言输入起点与终点；量词 * + ? {m,n} {m,}（0<=m<=n<=100 且 n>=1，
//   {m,} 要求 m<=100），量词后可再跟一个 '?' 表示懒惰。
//
// 约定：'{' 在原子位置视为字面字节；紧跟在原子之后时必须构成合法量词，否则语法错误。

type nodeKind int

const (
	kChar nodeKind = iota
	kAny
	kClass
	kAssertStart
	kAssertEnd
	kConcat
	kAlt
	kRepeat
)

type node struct {
	kind     nodeKind
	b        byte
	set      *[256]bool
	children []*node // kConcat 的项 / kAlt 的分支（每个分支为 kConcat）
	child    *node   // kRepeat 的作用对象
	min      int
	max      int // -1 表示无上限
	lazy     bool
}

type parser struct {
	s string
	i int
}

// parse 解析模式，语法错误返回 ErrSyntax。
func parse(pattern string) (*node, error) {
	p := &parser{s: pattern}
	e, err := p.parseAlt()
	if err != nil {
		return nil, err
	}
	if p.i != len(p.s) { // 存在未匹配的 ')'
		return nil, ErrSyntax
	}
	return e, nil
}

func (p *parser) parseAlt() (*node, error) {
	branches := []*node{}
	for {
		c, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		branches = append(branches, c)
		if p.i < len(p.s) && p.s[p.i] == '|' {
			p.i++
			continue
		}
		break
	}
	if len(branches) == 1 {
		return branches[0], nil
	}
	return &node{kind: kAlt, children: branches}, nil
}

func (p *parser) parseConcat() (*node, error) {
	items := []*node{}
	for p.i < len(p.s) && p.s[p.i] != '|' && p.s[p.i] != ')' {
		it, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return &node{kind: kConcat, children: items}, nil
}

func (p *parser) parseItem() (*node, error) {
	at, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	if p.i >= len(p.s) {
		return at, nil
	}
	var min, max int
	isQuant := true
	switch p.s[p.i] {
	case '*':
		min, max = 0, -1
		p.i++
	case '+':
		min, max = 1, -1
		p.i++
	case '?':
		min, max = 0, 1
		p.i++
	case '{':
		m, n, err := p.parseCount()
		if err != nil {
			return nil, err
		}
		min, max = m, n
	default:
		isQuant = false
	}
	if !isQuant {
		return at, nil
	}
	if at.kind == kAssertStart || at.kind == kAssertEnd {
		return nil, ErrSyntax // ^ 与 $ 后不得紧跟量词
	}
	lazy := false
	if p.i < len(p.s) && p.s[p.i] == '?' {
		lazy = true
		p.i++
	}
	// 连续两个量词（除懒惰后缀）为语法错误。
	if p.i < len(p.s) {
		switch p.s[p.i] {
		case '*', '+', '?':
			return nil, ErrSyntax
		}
	}
	return &node{kind: kRepeat, child: at, min: min, max: max, lazy: lazy}, nil
}

// parseCount 解析 {m,n} 或 {m,}；p.s[p.i] == '{'。任何形式不合法均为语法错误。
func (p *parser) parseCount() (int, int, error) {
	j := p.i + 1
	m, ok := scanCountDigits(p.s, &j)
	if !ok {
		return 0, 0, ErrSyntax
	}
	if j >= len(p.s) {
		return 0, 0, ErrSyntax
	}
	if p.s[j] == '}' { // {m} 不在语法中
		return 0, 0, ErrSyntax
	}
	if p.s[j] != ',' {
		return 0, 0, ErrSyntax
	}
	j++
	if j < len(p.s) && p.s[j] == '}' { // {m,}
		if m > 100 {
			return 0, 0, ErrSyntax
		}
		p.i = j + 1
		return m, -1, nil
	}
	n, ok := scanCountDigits(p.s, &j)
	if !ok || j >= len(p.s) || p.s[j] != '}' {
		return 0, 0, ErrSyntax
	}
	// {m,n}：0 <= m <= n <= 100 且 n >= 1
	if m > n || n > 100 || n < 1 {
		return 0, 0, ErrSyntax
	}
	p.i = j + 1
	return m, n, nil
}

// scanCountDigits 读取十进制数字，值超过 100000 时截断（随后范围检查必判为语法错误）。
func scanCountDigits(s string, j *int) (int, bool) {
	start := *j
	v := 0
	for *j < len(s) && s[*j] >= '0' && s[*j] <= '9' {
		v = v*10 + int(s[*j]-'0')
		if v > 100000 {
			v = 100001
		}
		*j++
	}
	return v, *j > start
}

func (p *parser) parseAtom() (*node, error) {
	c := p.s[p.i]
	switch c {
	case '(':
		p.i++
		e, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		if p.i >= len(p.s) || p.s[p.i] != ')' {
			return nil, ErrSyntax
		}
		p.i++
		return e, nil
	case '[':
		return p.parseClass()
	case '.':
		p.i++
		return &node{kind: kAny}, nil
	case '^':
		p.i++
		return &node{kind: kAssertStart}, nil
	case '$':
		p.i++
		return &node{kind: kAssertEnd}, nil
	case '\\':
		p.i++
		if p.i >= len(p.s) {
			return nil, ErrSyntax
		}
		b := p.s[p.i]
		p.i++
		return &node{kind: kChar, b: b}, nil
	case '*', '+', '?':
		return nil, ErrSyntax // 量词前没有原子
	default:
		p.i++
		return &node{kind: kChar, b: c}, nil
	}
}

// parseClass 解析字符集；p.s[p.i] == '['。集合内无转义，']' 直接结束集合。
func (p *parser) parseClass() (*node, error) {
	p.i++
	neg := false
	if p.i < len(p.s) && p.s[p.i] == '^' {
		neg = true
		p.i++
	}
	var set [256]bool
	count := 0
	for {
		if p.i >= len(p.s) {
			return nil, ErrSyntax // 未闭合
		}
		c := p.s[p.i]
		if c == ']' {
			p.i++
			break
		}
		lo := c
		p.i++
		if p.i < len(p.s) && p.s[p.i] == '-' && p.i+1 < len(p.s) && p.s[p.i+1] != ']' {
			hi := p.s[p.i+1]
			p.i += 2
			if lo > hi {
				return nil, ErrSyntax
			}
			for b := lo; ; b++ {
				if !set[b] {
					set[b] = true
					count++
				}
				if b == hi {
					break
				}
			}
		} else {
			if !set[lo] {
				set[lo] = true
				count++
			}
		}
	}
	if count == 0 {
		return nil, ErrSyntax // 空集合
	}
	if neg {
		for i := range set {
			set[i] = !set[i]
		}
	}
	return &node{kind: kClass, set: &set}, nil
}

// nullable 报告子树是否可匹配空串（存在一条不消耗字节的路径：
// 空选择支、纯断言、{0,n} 或 * 或 ? 子项等）。
func nullable(n *node) bool {
	switch n.kind {
	case kChar, kAny, kClass:
		return false
	case kAssertStart, kAssertEnd:
		return true
	case kConcat:
		for _, c := range n.children {
			if !nullable(c) {
				return false
			}
		}
		return true
	case kAlt:
		for _, b := range n.children {
			if nullable(b) {
				return true
			}
		}
		return false
	case kRepeat:
		return n.min == 0 || nullable(n.child)
	}
	return false
}

// hasNullableRepeat 报告是否存在作用于可空原子/分组的量词。
func hasNullableRepeat(n *node) bool {
	switch n.kind {
	case kRepeat:
		if nullable(n.child) {
			return true
		}
		return hasNullableRepeat(n.child)
	case kConcat, kAlt:
		for _, c := range n.children {
			if hasNullableRepeat(c) {
				return true
			}
		}
	}
	return false
}
