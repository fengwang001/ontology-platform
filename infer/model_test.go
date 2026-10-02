package infer

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// dumpLines 生成会话状态的规范文本表示，供实现与朴素模型逐位对照。
func dumpLines(L, nextID int, pruneOf func(i int) Type, levelOf func(id int) int,
	resolveOf func(i int) Type, env map[string]scheme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "L=%d next=%d\n", L, nextID)
	for i := 1; i < nextID; i++ {
		p := pruneOf(i)
		lv := "-"
		if p.IsVar {
			lv = strconv.Itoa(levelOf(p.ID))
		}
		fmt.Fprintf(&b, "v%d prune=%s level=%s resolved=%s\n", i, p, lv, resolveOf(i))
	}
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sc := env[n]
		fmt.Fprintf(&b, "env[%s] quant=%d body=%s\n", n, sc.quant, sc.body)
	}
	return b.String()
}

func dumpSession(s *Session) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return dumpLines(s.level, s.nextID,
		func(i int) Type { return s.prune(Var(i)) },
		func(id int) int { return s.vars[id-1].level },
		func(i int) Type { return s.resolve(Var(i)) },
		s.env)
}

// model 是按规格逐步写成的朴素模拟：显式替换映射、
// 每次全量重算层级调整、失败时整体恢复快照。
type model struct {
	ctors  map[string]int
	maxVar int
	L      int
	next   int
	levels map[int]int
	subst  map[int]Type
	env    map[string]scheme
}

func newModel(ctors map[string]int, maxVar int) *model {
	return &model{
		ctors:  ctors,
		maxVar: maxVar,
		next:   1,
		levels: map[int]int{},
		subst:  map[int]Type{},
		env:    map[string]scheme{},
	}
}

func (m *model) newVar() (Type, error) {
	if m.next-1 >= m.maxVar {
		return Type{}, ErrVarLimit
	}
	id := m.next
	m.next++
	m.levels[id] = m.L
	return Var(id), nil
}

func (m *model) enter() error {
	if m.L >= maxLevel {
		return ErrLevelLimit
	}
	m.L++
	return nil
}

func (m *model) leave() error {
	if m.L == 0 {
		return ErrLevelZero
	}
	m.L--
	return nil
}

func (m *model) validate(t Type) error {
	if t.IsVar {
		if t.ID < 1 || t.ID >= m.next {
			return ErrInvalidArgument
		}
		return nil
	}
	arity, ok := m.ctors[t.Name]
	if !ok {
		return ErrInvalidArgument
	}
	if len(t.Args) != arity {
		return ErrInvalidArgument
	}
	for _, a := range t.Args {
		if err := m.validate(a); err != nil {
			return err
		}
	}
	return nil
}

// substOnce 对 t 做一层显式替换。
func (m *model) substOnce(t Type) Type {
	if t.IsVar {
		if b, ok := m.subst[t.ID]; ok {
			return b
		}
		return t
	}
	args := make([]Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = m.substOnce(a)
	}
	return Type{Name: t.Name, Args: args}
}

// resolve 反复全量替换直到不动点（朴素实现）。
func (m *model) resolve(t Type) Type {
	for {
		next := m.substOnce(t)
		if typeEqual(next, t) {
			return next
		}
		t = next
	}
}

func typeEqual(a, b Type) bool {
	if a.IsVar != b.IsVar || a.ID != b.ID || a.Name != b.Name || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if !typeEqual(a.Args[i], b.Args[i]) {
			return false
		}
	}
	return true
}

func (m *model) prune(t Type) Type {
	for t.IsVar {
		b, ok := m.subst[t.ID]
		if !ok {
			return t
		}
		t = b
	}
	return t
}

// unify 先校验两侧，再递归合一；失败时整体恢复快照。
func (m *model) unify(a, b Type) error {
	if err := m.validate(a); err != nil {
		return err
	}
	if err := m.validate(b); err != nil {
		return err
	}
	savedSubst := make(map[int]Type, len(m.subst))
	for k, v := range m.subst {
		savedSubst[k] = v
	}
	savedLevels := make(map[int]int, len(m.levels))
	for k, v := range m.levels {
		savedLevels[k] = v
	}
	if err := m.unifyRec(a, b); err != nil {
		m.subst = savedSubst
		m.levels = savedLevels
		return err
	}
	return nil
}

func (m *model) unifyRec(a, b Type) error {
	pa, pb := m.prune(a), m.prune(b)
	if pa.IsVar && pb.IsVar && pa.ID == pb.ID {
		return nil
	}
	if pa.IsVar {
		return m.bindVar(pa.ID, pb)
	}
	if pb.IsVar {
		return m.bindVar(pb.ID, pa)
	}
	if pa.Name != pb.Name {
		return ErrMismatch
	}
	for i := range pa.Args {
		if err := m.unifyRec(pa.Args[i], pb.Args[i]); err != nil {
			return err
		}
	}
	return nil
}

func (m *model) bindVar(id int, t Type) error {
	if t.IsVar {
		m.subst[id] = t
		if m.levels[t.ID] > m.levels[id] {
			m.levels[t.ID] = m.levels[id]
		}
		return nil
	}
	r := m.resolve(t)
	if occurs(r, id) {
		return ErrOccurs
	}
	for _, u := range collectVars(r, nil) {
		if m.levels[u] > m.levels[id] {
			m.levels[u] = m.levels[id]
		}
	}
	m.subst[id] = t
	return nil
}

func (m *model) bind(name string, ty Type, expensive bool) error {
	if len(name) == 0 {
		return ErrInvalidArgument
	}
	if err := m.validate(ty); err != nil {
		return err
	}
	if _, ok := m.env[name]; ok {
		return ErrNameExists
	}
	if len(m.env) >= maxEnvSize {
		return ErrEnvLimit
	}
	r := m.resolve(ty)
	var quant []int
	seen := map[int]bool{}
	for _, id := range collectVars(r, nil) {
		if !seen[id] {
			seen[id] = true
			if m.levels[id] > m.L {
				quant = append(quant, id)
			}
		}
	}
	if expensive {
		for _, id := range quant {
			m.levels[id] = m.L
		}
		m.env[name] = scheme{body: r, quant: 0}
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
	m.env[name] = scheme{body: body, quant: len(quant)}
	return nil
}

func (m *model) lookup(name string) (Type, error) {
	if len(name) == 0 {
		return Type{}, ErrInvalidArgument
	}
	sc, ok := m.env[name]
	if !ok {
		return Type{}, ErrNameNotFound
	}
	if m.next-1+sc.quant > m.maxVar {
		return Type{}, ErrVarLimit
	}
	fresh := make([]int, sc.quant)
	for i := range fresh {
		fresh[i] = m.next
		m.next++
		m.levels[fresh[i]] = m.L
	}
	body := mapVars(sc.body, func(id int) Type {
		if id < 0 {
			return Var(fresh[-id-1])
		}
		return Var(id)
	})
	return body, nil
}

func (m *model) level(id int) (int, error) {
	if id < 1 || id >= m.next {
		return 0, ErrInvalidArgument
	}
	p := m.prune(Var(id))
	if !p.IsVar {
		return 0, ErrBound
	}
	return m.levels[p.ID], nil
}

func (m *model) dump() string {
	return dumpLines(m.L, m.next,
		func(i int) Type { return m.prune(Var(i)) },
		func(id int) int { return m.levels[id] },
		func(i int) Type { return m.resolve(Var(i)) },
		m.env)
}

// errKind 把错误归约为可比较的种类串。
func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrLevelZero):
		return "level-zero"
	case errors.Is(err, ErrVarLimit):
		return "var-limit"
	case errors.Is(err, ErrLevelLimit):
		return "level-limit"
	case errors.Is(err, ErrEnvLimit):
		return "env-limit"
	case errors.Is(err, ErrNameExists):
		return "name-exists"
	case errors.Is(err, ErrNameNotFound):
		return "name-not-found"
	case errors.Is(err, ErrBound):
		return "bound"
	case errors.Is(err, ErrOccurs):
		return "occurs"
	case errors.Is(err, ErrMismatch):
		return "mismatch"
	default:
		return "unknown:" + err.Error()
	}
}

// randOp 是一次随机操作的完整输入。
type randOp struct {
	kind      string
	a, b      Type
	name      string
	expensive bool
	id        int
}

func opDesc(op randOp) string {
	switch op.kind {
	case "newvar":
		return "NewVar()"
	case "enter":
		return "Enter()"
	case "leave":
		return "Leave()"
	case "unify":
		return fmt.Sprintf("Unify(%s, %s)", op.a, op.b)
	case "bind":
		return fmt.Sprintf("Bind(%q, %s, %v)", op.name, op.a, op.expensive)
	case "lookup":
		return fmt.Sprintf("Lookup(%q)", op.name)
	case "resolve":
		return fmt.Sprintf("Resolve(%s)", op.a)
	case "level":
		return fmt.Sprintf("Level(%d)", op.id)
	default:
		return "?"
	}
}

// randCtorTable 生成随机构造子表，保证含一个零元构造子。
func randCtorTable(r *rand.Rand) (map[string]int, []string) {
	pool := []struct {
		name  string
		arity int
	}{
		{"Bool", 0}, {"Str", 0}, {"List", 1}, {"Maybe", 1},
		{"Fn", 2}, {"Pair", 2}, {"T3", 3}, {"T4", 4},
	}
	arity := map[string]int{"Int": 0}
	names := []string{"Int"}
	extra := r.Intn(len(pool))
	for _, idx := range r.Perm(len(pool))[:extra] {
		arity[pool[idx].name] = pool[idx].arity
		names = append(names, pool[idx].name)
	}
	return arity, names
}

// genType 生成随机类型，depth 限制递归深度；小概率产生非法类型。
func genType(r *rand.Rand, names []string, arity map[string]int, maxVarID, depth int) Type {
	if r.Intn(100) < 4 {
		switch r.Intn(3) {
		case 0:
			return Var(maxVarID + 1 + r.Intn(3)) // 未知变量编号
		case 1:
			return Con("NoSuchCtor") // 未登记构造子
		default:
			for _, n := range names {
				if arity[n] > 0 {
					return Con(n) // 元数不符
				}
			}
			return Var(0)
		}
	}
	var zero []string
	for _, n := range names {
		if arity[n] == 0 {
			zero = append(zero, n)
		}
	}
	if depth == 0 || r.Intn(100) < 40 {
		if maxVarID > 0 && r.Intn(2) == 0 {
			return Var(1 + r.Intn(maxVarID))
		}
		return Con(zero[r.Intn(len(zero))])
	}
	n := names[r.Intn(len(names))]
	k := arity[n]
	if k == 0 {
		return Con(n)
	}
	args := make([]Type, k)
	for i := range args {
		args[i] = genType(r, names, arity, maxVarID, depth-1)
	}
	return Con(n, args...)
}

func bindName(r *rand.Rand, fresh *int) string {
	pool := []string{"a", "b", "c", "d", "e", "f"}
	switch x := r.Intn(100); {
	case x < 3:
		return ""
	case x < 15:
		*fresh++
		return fmt.Sprintf("fresh%d", *fresh)
	default:
		return pool[r.Intn(len(pool))]
	}
}

func lookupName(r *rand.Rand, fresh *int) string {
	pool := []string{"a", "b", "c", "d", "e", "f", "missing1", "missing2"}
	switch x := r.Intn(100); {
	case x < 3:
		return ""
	case x < 10:
		*fresh++
		return fmt.Sprintf("fresh%d", *fresh)
	default:
		return pool[r.Intn(len(pool))]
	}
}

func genOp(r *rand.Rand, names []string, arity map[string]int, maxVarID int, fresh *int) randOp {
	switch x := r.Intn(100); {
	case x < 15:
		return randOp{kind: "newvar"}
	case x < 25:
		return randOp{kind: "enter"}
	case x < 35:
		return randOp{kind: "leave"}
	case x < 60:
		return randOp{kind: "unify",
			a: genType(r, names, arity, maxVarID, 2),
			b: genType(r, names, arity, maxVarID, 2)}
	case x < 75:
		return randOp{kind: "bind", name: bindName(r, fresh),
			a: genType(r, names, arity, maxVarID, 2), expensive: r.Intn(2) == 0}
	case x < 88:
		return randOp{kind: "lookup", name: lookupName(r, fresh)}
	case x < 95:
		return randOp{kind: "resolve", a: genType(r, names, arity, maxVarID, 2)}
	default:
		id := 0
		if maxVarID > 0 {
			id = r.Intn(maxVarID + 2) // 可能为 0 或越界
		}
		return randOp{kind: "level", id: id}
	}
}

func applySession(s *Session, op randOp) (string, error) {
	switch op.kind {
	case "newvar":
		v, err := s.NewVar()
		if err != nil {
			return "", err
		}
		return v.String(), nil
	case "enter":
		return "", s.Enter()
	case "leave":
		return "", s.Leave()
	case "unify":
		return "", s.Unify(op.a, op.b)
	case "bind":
		return "", s.Bind(op.name, op.a, op.expensive)
	case "lookup":
		v, err := s.Lookup(op.name)
		if err != nil {
			return "", err
		}
		r, err := s.Resolve(v)
		if err != nil {
			return "", err
		}
		return r.String(), nil
	case "resolve":
		r, err := s.Resolve(op.a)
		if err != nil {
			return "", err
		}
		return r.String(), nil
	case "level":
		l, err := s.Level(op.id)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(l), nil
	default:
		panic("unknown op " + op.kind)
	}
}

func applyModel(m *model, op randOp) (string, error) {
	switch op.kind {
	case "newvar":
		v, err := m.newVar()
		if err != nil {
			return "", err
		}
		return v.String(), nil
	case "enter":
		return "", m.enter()
	case "leave":
		return "", m.leave()
	case "unify":
		return "", m.unify(op.a, op.b)
	case "bind":
		return "", m.bind(op.name, op.a, op.expensive)
	case "lookup":
		v, err := m.lookup(op.name)
		if err != nil {
			return "", err
		}
		return m.resolve(v).String(), nil
	case "resolve":
		if err := m.validate(op.a); err != nil {
			return "", err
		}
		return m.resolve(op.a).String(), nil
	case "level":
		l, err := m.level(op.id)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(l), nil
	default:
		panic("unknown op " + op.kind)
	}
}

func fnvHash(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// TestRandomModelComparison 用 2000 组随机操作序列对照实现与朴素模型：
// 每个操作的错误种类、返回值与操作后的完整状态都必须逐位一致。
// 每个操作的输入、输出与判定依据写入 infer/random_model.log。
func TestRandomModelComparison(t *testing.T) {
	f, err := os.Create("random_model.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := rand.New(rand.NewSource(1160))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		arity, names := randCtorTable(r)
		maxVar := []int{6, 12, 40, 200}[r.Intn(4)]
		s, err := NewSession(arity, maxVar)
		if err != nil {
			t.Fatal(err)
		}
		m := newModel(arity, maxVar)
		ops := 20 + r.Intn(30)
		fmt.Fprintf(f, "=== seq=%d maxVar=%d ctors=%v ops=%d\n", seq, maxVar, arity, ops)
		fresh := 0
		for i := 0; i < ops; i++ {
			op := genOp(r, names, arity, m.next-1, &fresh)
			desc := opDesc(op)
			sOut, sErr := applySession(s, op)
			mOut, mErr := applyModel(m, op)
			sKind, mKind := errKind(sErr), errKind(mErr)
			verdict := "MATCH"
			basis := "error-kind+output+state identical"
			switch {
			case sKind != mKind:
				verdict = "MISMATCH"
				basis = fmt.Sprintf("error kind: session=%s model=%s", sKind, mKind)
			case sOut != mOut:
				verdict = "MISMATCH"
				basis = fmt.Sprintf("output: session=%q model=%q", sOut, mOut)
			default:
				ds, dm := dumpSession(s), m.dump()
				if ds != dm {
					verdict = "MISMATCH"
					basis = fmt.Sprintf("state:\nsession:\n%s\nmodel:\n%s", ds, dm)
				}
			}
			fmt.Fprintf(f, "seq=%d op=%d %s => kind=%s out=%q verdict=%s basis=%s state=%s\n",
				seq, i, desc, sKind, sOut, verdict, basis, fnvHash(dumpSession(s)))
			if verdict != "MATCH" {
				t.Fatalf("seq=%d op=%d %s: %s", seq, i, desc, basis)
			}
		}
	}
	t.Logf("2000 random sequences matched the naive model; details in infer/random_model.log")
}
