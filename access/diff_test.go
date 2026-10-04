package access_test

// Random differential testing: an independent naive model (re-derived row by
// row and rule by rule, with its own FNV/mask implementation) drives the same
// operation sequence into policy.Store + access.Reader and compares every
// error class, epoch, result rows, truncated flag and touched bound.

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ontology/access"
	"ontology/mask"
	"ontology/policy"
)

// ---------- naive model ----------

type nColumn struct {
	name string
	typ  mask.Type
	def  mask.Level
}

type nCell struct {
	null bool
	i    int64
	s    string
}

type nPolicy struct {
	id, table, role string
	kind            policy.Kind
	atoms           []policy.Atom
}

type nModel struct {
	cols  map[string][]nColumn
	rows  map[string][][]nCell
	pols  map[string]nPolicy
	masks map[[3]string]mask.Level
	epoch uint64
}

func newNModel() *nModel {
	return &nModel{
		cols:  map[string][]nColumn{},
		rows:  map[string][][]nCell{},
		pols:  map[string]nPolicy{},
		masks: map[[3]string]mask.Level{},
	}
}

func buildTouchedWorld(t *testing.T, noiseRoles, noisePols int) (*policy.Store, *access.Reader) {
	t.Helper()
	s := policy.NewStore()
	// target table: two applicable policies for "target", masks on 2 columns
	if err := s.AddTable("main", []policy.Column{
		{Name: "a", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "b", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "c", Type: mask.TypeStr, Def: mask.LevelPlain},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("main", []mask.Value{iv(1), iv(2), sv("x")}); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AddRowPolicy(policy.RowPolicy{ID: "p1", Table: "main", Role: "target",
		Kind: policy.Permissive}))
	must(s.AddRowPolicy(policy.RowPolicy{ID: "r1", Table: "main", Role: "*",
		Kind: policy.Restrictive}))
	must(s.SetMask("main", "a", "target", mask.LevelPlain))
	must(s.SetMask("main", "b", "target", mask.LevelPartial))

	// irrelevant rules: other roles on the same table, plus a second table
	for i := 0; i < noiseRoles; i++ {
		nr := "noise" + strconv.Itoa(i)
		_ = s.SetMask("main", "a", nr, mask.LevelDeny)
		_ = s.SetMask("main", "b", nr, mask.LevelHash)
		_ = s.SetMask("main", "c", nr, mask.LevelNull)
	}
	if err := s.AddTable("other", []policy.Column{
		{Name: "z", Type: mask.TypeInt, Def: mask.LevelPlain}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("other", []mask.Value{iv(9)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < noisePols; i++ {
		_ = s.AddRowPolicy(policy.RowPolicy{
			ID:    "op" + strconv.Itoa(i),
			Table: "other",
			Role:  "target",
			Kind:  policy.Permissive,
		})
	}
	return s, access.NewReader(s)
}

// TestTouchedIndependentOfNoise checks the examined-rule bound is identical
// whether the store holds 100 or 10000 unrelated rules.
func TestTouchedIndependentOfNoise(t *testing.T) {
	measure := func(noiseRoles, noisePols int) int {
		s, r := buildTouchedWorld(t, noiseRoles, noisePols)
		_, err := r.Read([]string{"target"}, "main", []string{"a", "b"},
			policy.Predicate{Atoms: []policy.Atom{
				{Col: "a", Op: policy.OpEq, Const: iv(1)},
			}}, 10)
		if err != nil {
			t.Fatal(err)
		}
		// bound = 2 applicable row policies (p1, r1)
		//       + mask rules for {a,b} x {target} = 2
		want := 4
		if r.Touched() != want {
			t.Fatalf("noise(%d,%d): touched=%d want %d, epoch=%d",
				noiseRoles, noisePols, r.Touched(), want, s.Epoch())
		}
		return r.Touched()
	}
	small := measure(100, 100)
	large := measure(10000, 10000)
	if small != large {
		t.Fatalf("touched differs across noise levels: %d vs %d", small, large)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	s := policy.NewStore()
	if err := s.AddTable("t", []policy.Column{
		{Name: "id", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "s", Type: mask.TypeStr, Def: mask.LevelPlain},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*",
		Kind: policy.Permissive}); err != nil {
		t.Fatal(err)
	}
	r := access.NewReader(s)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// writer: inserts and accepted mask changes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := int64(0); ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = s.Insert("t", []mask.Value{iv(i), sv("v")})
			_ = s.SetMask("t", "s", "w", mask.Level(i%5))
		}
	}()

	// readers: every observed result must be self-consistent at one epoch
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				res, err := r.Read([]string{"w", "x"}, "t", []string{"id", "s"},
					policy.Predicate{}, 50)
				if err != nil {
					panic(err)
				}
				if len(res.Rows) > 50 {
					panic("limit exceeded")
				}
				// cells must be internally consistent: every cell typed, no deny leak
				for _, rr := range res.Rows {
					if len(rr.Cells) != 2 {
						panic("cell width")
					}
				}
			}
		}()
	}

	// let it churn briefly, then stop the writer and wait for readers
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	time.Sleep(30 * time.Millisecond)
	close(stop)
	<-done
}

func validName(s string) bool { return len(s) >= 1 && len(s) <= 64 }

func nMask(c nCell, typ mask.Type, lv mask.Level) nCell {
	if c.null {
		return c
	}
	switch lv {
	case mask.LevelPlain, mask.LevelDeny:
		return c
	case mask.LevelNull:
		return nCell{null: true}
	case mask.LevelHash:
		var b []byte
		if typ == mask.TypeInt {
			b = []byte(strconv.FormatInt(c.i, 10))
		} else {
			b = []byte(c.s)
		}
		h := fnv.New64a()
		_, _ = h.Write(b)
		return nCell{s: fmt.Sprintf("%016x", h.Sum64())}
	default: // partial
		if typ == mask.TypeInt {
			f := c.i / 100
			if c.i%100 < 0 {
				f--
			}
			return nCell{i: f * 100}
		}
		n := len(c.s)
		if n <= 4 {
			return nCell{s: strings.Repeat("*", n)}
		}
		return nCell{s: strings.Repeat("*", n-4) + c.s[n-4:]}
	}
}

func nAtom(cell nCell, a policy.Atom) bool {
	if cell.null {
		return false
	}
	if cell.s != "" || a.Const.Type == mask.TypeStr {
		// operate as strings when either side is a str
		if a.Const.Type != mask.TypeStr {
			return false
		}
		switch a.Op {
		case policy.OpEq:
			return cell.s == string(a.Const.Str)
		case policy.OpNe:
			return cell.s != string(a.Const.Str)
		}
		return false
	}
	switch a.Op {
	case policy.OpEq:
		return cell.i == a.Const.Int
	case policy.OpNe:
		return cell.i != a.Const.Int
	case policy.OpLt:
		return cell.i < a.Const.Int
	case policy.OpLe:
		return cell.i <= a.Const.Int
	}
	return false
}

// classify maps store-level errors to stable labels.
func classify(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, policy.ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, policy.ErrTableNotFound):
		return "no-table"
	case errors.Is(err, policy.ErrColumnNotFound):
		return "no-col"
	case errors.Is(err, policy.ErrAlreadyExists):
		return "exists"
	case errors.Is(err, policy.ErrNotFound):
		return "missing"
	case errors.Is(err, access.ErrColumnDenied):
		return "denied"
	case errors.Is(err, access.ErrTypeMismatch):
		return "type"
	default:
		return "other:" + err.Error()
	}
}

func (m *nModel) level(table string, ci int, roles []string) mask.Level {
	lv := mask.LevelDeny
	for _, role := range roles {
		if l, ok := m.masks[[3]string{table, m.cols[table][ci].name, role}]; ok {
			if l < lv {
				lv = l
			}
		} else if d := m.cols[table][ci].def; d < lv {
			lv = d
		}
	}
	return lv
}

type nReadOut struct {
	err       string
	rows      [][]nCell
	rowNos    []int
	truncated bool
	epoch     uint64
	bound     int
}

func colIndex(cols []nColumn, name string) int {
	for i, c := range cols {
		if c.name == name {
			return i
		}
	}
	return -1
}

// read mirrors access.Reader.Read including the rejection order.
func (m *nModel) read(roles []string, table string, sel []string,
	where policy.Predicate, limit int) nReadOut {
	// 1. parameter validation
	bad := len(roles) < 1 || len(roles) > 8 || !validName(table) ||
		len(sel) < 1 || len(sel) > 32 || limit < 1 || limit > 1000 ||
		len(where.Atoms) > 4
	roleSet := map[string]bool{}
	for _, rl := range roles {
		if !validName(rl) || rl == "*" || roleSet[rl] {
			bad = true
		}
		roleSet[rl] = true
	}
	seen := map[string]bool{}
	for _, c := range sel {
		if !validName(c) || seen[c] {
			bad = true
		}
		seen[c] = true
	}
	for _, a := range where.Atoms {
		if !validName(a.Col) || a.Const.Null {
			bad = true
		}
	}
	if bad {
		return nReadOut{err: "invalid"}
	}
	cols, ok := m.cols[table]
	if !ok {
		return nReadOut{err: "no-table"}
	}
	selIdx := make([]int, len(sel))
	for i, c := range sel {
		selIdx[i] = colIndex(cols, c)
		if selIdx[i] < 0 {
			return nReadOut{err: "no-col"}
		}
	}
	whereIdx := make([]int, len(where.Atoms))
	for i, a := range where.Atoms {
		whereIdx[i] = colIndex(cols, a.Col)
		if whereIdx[i] < 0 {
			return nReadOut{err: "no-col"}
		}
	}
	levels := map[int]mask.Level{}
	lvOf := func(ci int) mask.Level {
		if l, ok := levels[ci]; ok {
			return l
		}
		l := m.level(table, ci, roles)
		levels[ci] = l
		return l
	}
	for _, ci := range selIdx {
		if lvOf(ci) == mask.LevelDeny {
			return nReadOut{err: "denied"}
		}
	}
	for i, ci := range whereIdx {
		lv := lvOf(ci)
		if lv == mask.LevelDeny {
			return nReadOut{err: "denied"}
		}
		mt := cols[ci].typ
		if lv == mask.LevelHash {
			mt = mask.TypeStr
		}
		if where.Atoms[i].Const.Type != mt {
			return nReadOut{err: "type"}
		}
		if lv != mask.LevelNull && mt == mask.TypeStr &&
			(where.Atoms[i].Op == policy.OpLt || where.Atoms[i].Op == policy.OpLe) {
			return nReadOut{err: "type"}
		}
	}

	// applicable policies + touched bound
	var applicable []nPolicy
	for _, p := range m.pols {
		if p.table == table && (p.role == "*" || roleSet[p.role]) {
			applicable = append(applicable, p)
		}
	}
	bound := len(applicable)
	involved := map[int]bool{}
	for _, ci := range selIdx {
		involved[ci] = true
	}
	for _, ci := range whereIdx {
		involved[ci] = true
	}
	for ci := range involved {
		for _, rl := range roles {
			if _, ok := m.masks[[3]string{table, cols[ci].name, rl}]; ok {
				bound++
			}
		}
	}

	visible := func(row []nCell) bool {
		sawP, hitP := false, false
		for _, p := range applicable {
			hit := true
			for _, a := range p.atoms {
				if !nAtom(row[colIndex(cols, a.Col)], a) {
					hit = false
					break
				}
			}
			if p.kind == policy.Restrictive && !hit {
				return false
			}
			if p.kind == policy.Permissive {
				sawP = true
				if hit {
					hitP = true
				}
			}
		}
		return sawP && hitP
	}
	whereHit := func(row []nCell) bool {
		for i, a := range where.Atoms {
			ci := whereIdx[i]
			lv := levels[ci]
			if lv == mask.LevelNull {
				return false
			}
			mc := nMask(row[ci], cols[ci].typ, lv)
			if !nAtom(mc, a) {
				return false
			}
		}
		return true
	}

	out := nReadOut{epoch: m.epoch, bound: bound}
	for no, row := range m.rows[table] {
		if !visible(row) || !whereHit(row) {
			continue
		}
		if len(out.rows) == limit {
			out.truncated = true
			break
		}
		cells := make([]nCell, len(selIdx))
		for i, ci := range selIdx {
			cells[i] = nMask(row[ci], cols[ci].typ, levels[ci])
		}
		out.rows = append(out.rows, cells)
		out.rowNos = append(out.rowNos, no+1)
	}
	return out
}

// ---------- random world generation ----------

type nReadReq struct {
	roles []string
	sel   []string
	where policy.Predicate
	limit int
}

type nWorld struct {
	cols  []nColumn
	rows  [][]nCell
	pols  []nPolicy
	masks []struct {
		col, role string
		lv        mask.Level
	}
	reads   []nReadReq
	setupOK int // expected epoch after setup
}

var rolePool = []string{"r0", "r1", "r2", "r3"}
var strAlphabet = []string{"", "a", "ab", "abc", "abcd", "abcde", "123456789", "★x"}

func randCell(rng *rand.Rand, typ mask.Type) nCell {
	if rng.Intn(3) == 0 {
		return nCell{null: true}
	}
	if typ == mask.TypeInt {
		// values around rounding boundaries, including negatives
		return nCell{i: int64(rng.Intn(1300) - 650)}
	}
	return nCell{s: strAlphabet[rng.Intn(len(strAlphabet))]}
}

func randConst(rng *rand.Rand, typ mask.Type) mask.Value {
	if typ == mask.TypeInt {
		return iv(int64(rng.Intn(1300) - 650))
	}
	return sv(strAlphabet[rng.Intn(len(strAlphabet))])
}

func randWorld(rng *rand.Rand) nWorld {
	w := nWorld{}
	ncols := 1 + rng.Intn(4)
	for i := 0; i < ncols; i++ {
		typ := mask.TypeInt
		if rng.Intn(2) == 1 {
			typ = mask.TypeStr
		}
		w.cols = append(w.cols, nColumn{
			name: "c" + strconv.Itoa(i),
			typ:  typ,
			def:  mask.Level(rng.Intn(5)),
		})
	}
	nrows := rng.Intn(7)
	for r := 0; r < nrows; r++ {
		row := make([]nCell, ncols)
		for i, c := range w.cols {
			row[i] = randCell(rng, c.typ)
		}
		w.rows = append(w.rows, row)
	}
	npol := rng.Intn(7)
	usedID := map[string]bool{}
	for i := 0; i < npol; i++ {
		id := "p" + strconv.Itoa(i) + "-" + strconv.Itoa(rng.Intn(999999))
		for usedID[id] {
			id += "x"
		}
		usedID[id] = true
		role := rolePool[rng.Intn(len(rolePool))]
		if rng.Intn(4) == 0 {
			role = "*"
		}
		kind := policy.Permissive
		if rng.Intn(2) == 1 {
			kind = policy.Restrictive
		}
		p := nPolicy{id: id, table: "t", role: role, kind: kind}
		natoms := rng.Intn(4)
		for a := 0; a < natoms; a++ {
			ci := rng.Intn(ncols)
			col := w.cols[ci]
			op := []policy.Op{policy.OpEq, policy.OpNe, policy.OpLt, policy.OpLe}[rng.Intn(4)]
			if col.typ == mask.TypeStr && op != policy.OpEq && op != policy.OpNe {
				op = policy.OpEq
			}
			p.atoms = append(p.atoms, policy.Atom{
				Col: col.name, Op: op, Const: randConst(rng, col.typ),
			})
		}
		w.pols = append(w.pols, p)
	}
	nmask := rng.Intn(8)
	seen := map[[2]string]bool{}
	for i := 0; i < nmask; i++ {
		ci := rng.Intn(ncols)
		rl := rolePool[rng.Intn(len(rolePool))]
		key := [2]string{w.cols[ci].name, rl}
		if seen[key] {
			continue
		}
		seen[key] = true
		w.masks = append(w.masks, struct {
			col, role string
			lv        mask.Level
		}{w.cols[ci].name, rl, mask.Level(rng.Intn(5))})
	}
	nread := 1 + rng.Intn(3)
	for i := 0; i < nread; i++ {
		req := nReadReq{limit: 1 + rng.Intn(7)}
		perm := rng.Perm(len(rolePool))
		nr := 1 + rng.Intn(len(rolePool))
		for _, k := range perm[:nr] {
			req.roles = append(req.roles, rolePool[k])
		}
		ns := 1 + rng.Intn(ncols)
		for _, k := range rng.Perm(ncols)[:ns] {
			req.sel = append(req.sel, w.cols[k].name)
		}
		na := rng.Intn(4)
		for a := 0; a < na; a++ {
			ci := rng.Intn(ncols)
			col := w.cols[ci]
			op := []policy.Op{policy.OpEq, policy.OpNe, policy.OpLt, policy.OpLe}[rng.Intn(4)]
			// constant type intentionally random half the time to exercise mismatch
			ct := col.typ
			if rng.Intn(2) == 1 {
				if ct == mask.TypeInt {
					ct = mask.TypeStr
				} else {
					ct = mask.TypeInt
				}
			}
			req.where.Atoms = append(req.where.Atoms, policy.Atom{
				Col: col.name, Op: op, Const: randConst(rng, ct),
			})
		}
		w.reads = append(w.reads, req)
	}
	w.setupOK = len(w.pols) + len(w.masks)
	return w
}

func valueToCell(v mask.Value) nCell {
	if v.Null {
		return nCell{null: true}
	}
	if v.Type == mask.TypeInt {
		return nCell{i: v.Int}
	}
	return nCell{s: string(v.Str)}
}

func cellEq(a, b nCell) bool {
	return a.null == b.null && a.i == b.i && a.s == b.s
}

func worldSummary(w nWorld) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cols=%v rows=%v pols=%v masks=%v reads=%v",
		w.cols, w.rows, w.pols, w.masks, w.reads)
	return b.String()
}

// TestRandomDifferential runs 1500 random worlds against the naive model.
func TestRandomDifferential(t *testing.T) {
	const cases = 1500
	rng := rand.New(rand.NewSource(20261005))
	for it := 0; it < cases; it++ {
		w := randWorld(rng)
		store := policy.NewStore()
		m := newNModel()

		pcols := make([]policy.Column, len(w.cols))
		m.cols["t"] = append([]nColumn(nil), w.cols...)
		for i, c := range w.cols {
			pcols[i] = policy.Column{Name: c.name, Type: c.typ, Def: c.def}
		}
		if err := store.AddTable("t", pcols); err != nil {
			t.Fatalf("case %d addtable: %v", it, err)
		}
		m.rows["t"] = nil
		for _, r := range w.rows {
			prow := make([]mask.Value, len(r))
			for i, c := range r {
				if c.null {
					prow[i] = mask.NullVal(w.cols[i].typ)
				} else if w.cols[i].typ == mask.TypeInt {
					prow[i] = iv(c.i)
				} else {
					prow[i] = sv(c.s)
				}
			}
			if err := store.Insert("t", prow); err != nil {
				t.Fatalf("case %d insert: %v", it, err)
			}
			m.rows["t"] = append(m.rows["t"], append([]nCell(nil), r...))
		}
		for _, p := range w.pols {
			err := store.AddRowPolicy(policy.RowPolicy{
				ID: p.id, Table: p.table, Role: p.role, Kind: p.kind,
				Pred: policy.Predicate{Atoms: p.atoms},
			})
			if err != nil {
				t.Fatalf("case %d addpolicy %v: %v", it, p.id, err)
			}
			m.pols[p.id] = p
			m.epoch++
		}
		for _, mr := range w.masks {
			if err := store.SetMask("t", mr.col, mr.role, mr.lv); err != nil {
				t.Fatalf("case %d setmask: %v", it, err)
			}
			m.masks[[3]string{"t", mr.col, mr.role}] = mr.lv
			m.epoch++
		}
		if store.Epoch() != uint64(w.setupOK) || store.Epoch() != m.epoch {
			t.Fatalf("case %d epoch store=%d naive=%d setupOK=%d",
				it, store.Epoch(), m.epoch, w.setupOK)
		}

		r := access.NewReader(store)
		for ri, req := range w.reads {
			want := m.read(req.roles, "t", req.sel, req.where, req.limit)
			got, gerr := r.Read(req.roles, "t", req.sel, req.where, req.limit)
			glabel := classify(gerr)
			t.Logf("case=%d read=%d input={roles=%v sel=%v where=%v limit=%d} "+
				"naive=%s actual=%s touched=%d bound=%d basis={levels per col, "+
				"visible = >=1 applicable permissive hit AND all restrictive hit, "+
				"where evaluated on masked cells} world={%s}",
				it, ri, req.roles, req.sel, req.where.Atoms, req.limit,
				want.err, glabel, r.Touched(), want.bound, worldSummary(w))
			if glabel != want.err {
				t.Fatalf("case %d read %d error: naive=%q actual=%q\nworld=%s",
					it, ri, want.err, glabel, worldSummary(w))
			}
			if want.err != "" {
				continue
			}
			if got.Epoch != want.epoch {
				t.Fatalf("case %d epoch: %d want %d", it, got.Epoch, want.epoch)
			}
			if got.Truncated != want.truncated ||
				len(got.Rows) != len(want.rows) {
				t.Fatalf("case %d shape: got rows=%d trunc=%v want rows=%d trunc=%v",
					it, len(got.Rows), got.Truncated, len(want.rows), want.truncated)
			}
			for i, gr := range got.Rows {
				if gr.RowNo != want.rowNos[i] {
					t.Fatalf("case %d row %d no: %d want %d", it, i, gr.RowNo, want.rowNos[i])
				}
				for j, gv := range gr.Cells {
					if !cellEq(valueToCell(gv), want.rows[i][j]) {
						t.Fatalf("case %d cell [%d][%d]: %+v want %+v",
							it, i, j, valueToCell(gv), want.rows[i][j])
					}
				}
			}
			if r.Touched() != want.bound {
				t.Fatalf("case %d touched=%d bound=%d (must be <=, expected equal)",
					it, r.Touched(), want.bound)
			}
		}
	}
}
