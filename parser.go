package ontology

type opCode byte

const (
	opChar opCode = iota
	opAny
	opClass
	opStartAssert
	opEndAssert
	opSplit
	opJmp
	opMatch
)

type instruction struct {
	op     opCode
	byte   byte
	mask   [256]bool
	first  int
	second int
}

type program struct {
	code []instruction
}

type parser struct {
	pattern []byte
	pos     int
}

type node interface {
	nullable() bool
}

type concatNode struct {
	children []node
}

type altNode struct {
	left  node
	right node
}

type repeatNode struct {
	child     node
	min       int
	max       int
	unbounded bool
	lazy      bool
}

type charNode struct {
	value byte
	any   bool
	mask  [256]bool
	class bool
}

type assertNode struct {
	start bool
}

type groupNode struct {
	child node
}

func parsePattern(pattern []byte) (node, error) {
	p := &parser{pattern: pattern}
	root, err := p.parseAlternation()
	if err != nil {
		return nil, err
	}
	if p.pos != len(pattern) {
		return nil, ErrPatternSyntax
	}
	return root, nil
}

func (p *parser) parseAlternation() (node, error) {
	left, err := p.parseConcatenation()
	if err != nil {
		return nil, err
	}
	if p.pos >= len(p.pattern) || p.pattern[p.pos] != '|' {
		return left, nil
	}
	p.pos++
	right, err := p.parseAlternation()
	if err != nil {
		return nil, err
	}
	return &altNode{left: left, right: right}, nil
}

func (p *parser) parseConcatenation() (node, error) {
	var children []node
	for p.pos < len(p.pattern) {
		switch p.pattern[p.pos] {
		case '|', ')':
			if len(children) == 0 {
				return &concatNode{}, nil
			}
			return &concatNode{children: children}, nil
		}
		child, err := p.parseQuantified()
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return &concatNode{children: children}, nil
}

func (p *parser) parseQuantified() (node, error) {
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	_, atomIsAssertion := atom.(*assertNode)

	quantified := false
	repeat := repeatNode{child: atom}
	if p.pos >= len(p.pattern) {
		return atom, nil
	}

	switch p.pattern[p.pos] {
	case '*':
		repeat.min, repeat.max, repeat.unbounded = 0, 0, true
	case '+':
		repeat.min, repeat.max, repeat.unbounded = 1, 0, true
	case '?':
		repeat.min, repeat.max = 0, 1
	case '{':
		start := p.pos
		p.pos++
		m, ok := p.readDecimal()
		if !ok {
			p.pos = start
			return atom, nil
		}
		validShape := false
		if p.pos >= len(p.pattern) {
			p.pos = start
			return atom, nil
		}
		if p.pattern[p.pos] == ',' {
			p.pos++
			n, ok := p.readDecimal()
			if !ok {
				if p.pos >= len(p.pattern) || p.pattern[p.pos] != '}' {
					p.pos = start
					return atom, nil
				}
				validShape = true
				if m > 100 {
					return nil, ErrPatternSyntax
				}
				repeat.min, repeat.max, repeat.unbounded = m, 0, true
			} else {
				validShape = true
				if m > n || n < 1 || n > 100 {
					return nil, ErrPatternSyntax
				}
				repeat.min, repeat.max = m, n
			}
		} else {
			validShape = false
			repeat.min, repeat.max = m, m
		}
		if p.pos >= len(p.pattern) || p.pattern[p.pos] != '}' {
			p.pos = start
			return atom, nil
		}
		if !validShape {
			p.pos = start
			return atom, nil
		}
	default:
		return atom, nil
	}

	p.pos++
	if p.pos < len(p.pattern) && p.pattern[p.pos] == '?' {
		repeat.lazy = true
		p.pos++
	}
	quantified = true
	if atomIsAssertion {
		return nil, ErrPatternSyntax
	}

	if p.pos < len(p.pattern) {
		switch p.pattern[p.pos] {
		case '*', '+', '?', '{':
			return nil, ErrPatternSyntax
		}
	}
	if repeat.child.nullable() {
		return nil, ErrNullableRepeat
	}
	if !quantified {
		return atom, nil
	}
	return &repeat, nil
}

func (p *parser) readDecimal() (int, bool) {
	start := p.pos
	value := 0
	for p.pos < len(p.pattern) && p.pattern[p.pos] >= '0' && p.pattern[p.pos] <= '9' {
		value = value*10 + int(p.pattern[p.pos]-'0')
		p.pos++
	}
	return value, p.pos > start
}

func (p *parser) parseAtom() (node, error) {
	switch p.pattern[p.pos] {
	case '(':
		p.pos++
		child, err := p.parseAlternation()
		if err != nil {
			return nil, err
		}
		if p.pos >= len(p.pattern) || p.pattern[p.pos] != ')' {
			return nil, ErrPatternSyntax
		}
		p.pos++
		return &groupNode{child: child}, nil
	case '[':
		return p.parseClass()
	case '.':
		p.pos++
		return &charNode{any: true}, nil
	case '^':
		p.pos++
		return &assertNode{start: true}, nil
	case '$':
		p.pos++
		return &assertNode{start: false}, nil
	case '\\':
		p.pos++
		if p.pos >= len(p.pattern) {
			return nil, ErrPatternSyntax
		}
		value := p.pattern[p.pos]
		p.pos++
		return &charNode{value: value}, nil
	default:
		value := p.pattern[p.pos]
		p.pos++
		return &charNode{value: value}, nil
	}
}

func (p *parser) parseClass() (node, error) {
	p.pos++
	negated := false
	if p.pos < len(p.pattern) && p.pattern[p.pos] == '^' {
		negated = true
		p.pos++
	}

	mask := [256]bool{}
	for p.pos >= len(p.pattern) || p.pattern[p.pos] != ']' {
		if p.pos >= len(p.pattern) {
			return nil, ErrPatternSyntax
		}
		lo := p.pattern[p.pos]
		p.pos++
		if p.pos+1 < len(p.pattern) && p.pattern[p.pos] == '-' && p.pattern[p.pos+1] != ']' {
			p.pos++
			hi := p.pattern[p.pos]
			p.pos++
			if lo > hi {
				return nil, ErrPatternSyntax
			}
			for value := int(lo); value <= int(hi); value++ {
				mask[byte(value)] = true
			}
		} else {
			mask[lo] = true
		}
	}
	p.pos++

	if negated {
		for value := range mask {
			mask[value] = !mask[value]
		}
	}
	return &charNode{mask: mask, class: true}, nil
}

func (n *concatNode) nullable() bool {
	for _, child := range n.children {
		if !child.nullable() {
			return false
		}
	}
	return true
}

func (n *altNode) nullable() bool {
	return n.left.nullable() || n.right.nullable()
}

func (n *repeatNode) nullable() bool {
	return n.min == 0 || n.child.nullable()
}

func (n *charNode) nullable() bool   { return false }
func (n *assertNode) nullable() bool { return true }
func (n *groupNode) nullable() bool  { return n.child.nullable() }

type compiler struct {
	code  []instruction
	limit int
	err   error
}

func compilePattern(root node, limit int) (*program, error) {
	c := &compiler{limit: limit}
	c.compile(root)
	c.emit(instruction{op: opMatch})
	if c.err != nil {
		return nil, c.err
	}
	return &program{code: c.code}, nil
}

func (c *compiler) emit(inst instruction) int {
	index := len(c.code)
	if index >= c.limit {
		c.err = ErrProgramTooLarge
		return index
	}
	c.code = append(c.code, inst)
	return index
}

func (c *compiler) compile(n node) {
	if c.err != nil {
		return
	}
	switch v := n.(type) {
	case *concatNode:
		for _, child := range v.children {
			c.compile(child)
			if c.err != nil {
				return
			}
		}
	case *altNode:
		splitIndex := c.emit(instruction{op: opSplit})
		if c.err != nil {
			return
		}
		c.compile(v.left)
		if c.err != nil {
			return
		}
		jumpIndex := c.emit(instruction{op: opJmp})
		if c.err != nil {
			return
		}
		rightStart := len(c.code)
		c.compile(v.right)
		if c.err != nil {
			return
		}
		exit := len(c.code)
		c.code[splitIndex].first = splitIndex + 1
		c.code[splitIndex].second = rightStart
		c.code[jumpIndex].first = exit
	case *repeatNode:
		for range v.min {
			c.compile(v.child)
			if c.err != nil {
				return
			}
		}
		if v.unbounded {
			head := len(c.code)
			splitIndex := c.emit(instruction{op: opSplit})
			if c.err != nil {
				return
			}
			c.compile(v.child)
			if c.err != nil {
				return
			}
			jumpIndex := c.emit(instruction{op: opJmp})
			if c.err != nil {
				return
			}
			exit := len(c.code)
			c.code[jumpIndex].first = head
			if v.lazy {
				c.code[splitIndex].first, c.code[splitIndex].second = exit, splitIndex+1
			} else {
				c.code[splitIndex].first, c.code[splitIndex].second = splitIndex+1, exit
			}
		} else {
			var optionalStarts []int
			for range v.max - v.min {
				optionalStarts = append(optionalStarts, len(c.code))
				c.emit(instruction{op: opSplit})
				if c.err != nil {
					return
				}
				c.compile(v.child)
				if c.err != nil {
					return
				}
			}
			exit := len(c.code)
			for _, splitIndex := range optionalStarts {
				bodyStart := splitIndex + 1
				if v.lazy {
					c.code[splitIndex].first, c.code[splitIndex].second = exit, bodyStart
				} else {
					c.code[splitIndex].first, c.code[splitIndex].second = bodyStart, exit
				}
			}
		}
	case *groupNode:
		c.compile(v.child)
	case *charNode:
		switch {
		case v.any:
			c.emit(instruction{op: opAny})
		case v.class:
			c.emit(instruction{op: opClass, mask: v.mask})
		default:
			c.emit(instruction{op: opChar, byte: v.value})
		}
	case *assertNode:
		if v.start {
			c.emit(instruction{op: opStartAssert})
		} else {
			c.emit(instruction{op: opEndAssert})
		}
	}
	if c.err != nil {
		return
	}
}
