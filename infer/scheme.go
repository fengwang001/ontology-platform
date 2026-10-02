package infer

import "fmt"

// Bind 在当前层级 L 下把类型泛化后存入环境。
//
// 先取类型的完全解析形式 R，再取 R 中层级严格大于 L 的未绑定变量，
// 按它们在 R 中从左到右先序首次出现的次序排成量化列表（重复只记一次）。
// expensive 为假时量化这些变量（模式内按列表次序重编为 G0、G1……，
// 与原变量解除关联，原变量保持原状）；expensive 为真（值限制）时不量化，
// 而把这些变量的层级都降为 L，模式保留原变量。
//
// 拒绝次序：参数非法（空名字或类型非法）优先，然后 ErrNameExists，
// 最后 ErrEnvLimit。被拒绝时不改变任何状态。
func (s *Session) Bind(name string, t Type, expensive bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(name) == 0 {
		return fmt.Errorf("infer: bind: empty name: %w", ErrInvalidArgument)
	}
	if err := s.validate(t); err != nil {
		return err
	}
	if _, ok := s.env[name]; ok {
		return fmt.Errorf("infer: bind %q: %w", name, ErrNameExists)
	}
	if len(s.env) >= maxEnvSize {
		return fmt.Errorf("infer: bind %q: %w", name, ErrEnvLimit)
	}
	r := s.resolve(t)
	quant := s.quantifiable(r)
	if expensive {
		for _, id := range quant {
			s.vars[id-1].level = s.level
		}
		s.env[name] = scheme{body: r, quant: 0}
		return nil
	}
	index := make(map[int]int, len(quant))
	for i, id := range quant {
		index[id] = -(i + 1)
	}
	body := mapVars(r, func(id int) Type {
		if enc, ok := index[id]; ok {
			return Var(enc)
		}
		return Var(id)
	})
	s.env[name] = scheme{body: body, quant: len(quant)}
	return nil
}

// Lookup 返回名字对应模式的实例：每个量化变量按量化次序依次换成
// 新变量（层级为当前 L，编号连续递增），未量化部分与原变量共享。
//
// 拒绝次序：参数非法（空名字）优先，然后 ErrNameNotFound，
// 最后 ErrVarLimit。被拒绝时不消耗变量编号。
func (s *Session) Lookup(name string) (Type, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(name) == 0 {
		return Type{}, fmt.Errorf("infer: lookup: empty name: %w", ErrInvalidArgument)
	}
	sc, ok := s.env[name]
	if !ok {
		return Type{}, fmt.Errorf("infer: lookup %q: %w", name, ErrNameNotFound)
	}
	if len(s.vars)+sc.quant > s.maxVar {
		return Type{}, fmt.Errorf("infer: lookup %q: %w", name, ErrVarLimit)
	}
	fresh := make([]int, sc.quant)
	for i := range fresh {
		fresh[i] = s.nextID
		s.nextID++
		s.vars = append(s.vars, varInfo{level: s.level})
	}
	body := mapVars(sc.body, func(id int) Type {
		if id < 0 {
			return Var(fresh[-id-1])
		}
		return Var(id)
	})
	return body, nil
}

// quantifiable 返回 R 中层级严格大于当前 L 的未绑定变量，
// 按从左到右先序首次出现排序，重复只记一次。调用方须持有锁。
func (s *Session) quantifiable(r Type) []int {
	var out []int
	seen := make(map[int]bool)
	var walk func(t Type)
	walk = func(t Type) {
		if t.IsVar {
			if !seen[t.ID] {
				seen[t.ID] = true
				if s.vars[t.ID-1].level > s.level {
					out = append(out, t.ID)
				}
			}
			return
		}
		for _, a := range t.Args {
			walk(a)
		}
	}
	walk(r)
	return out
}
