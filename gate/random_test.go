package gate_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/compat"
	"ontology/gate"
	"ontology/schema"
)

// ---- 朴素模拟：独立的状态机实现，逐字段线性扫描，用于对照 ----

type nField struct {
	name       string
	typ        schema.Type
	required   bool
	hasDefault bool
}

type nSub struct {
	pinned    int
	fields    []string
	hasWaiver bool
	until     int64
	lagging   bool
	laggingAt int
}

type nSubject struct {
	mode     schema.Mode
	versions [][]nField
	subs     map[string]*nSub
}

type naive struct {
	hasNow   bool
	maxNow   int64
	subjects map[string]*nSubject
}

func newNaive() *naive {
	return &naive{subjects: map[string]*nSubject{}}
}

func naivePromotable(from, to schema.Type) bool {
	if from == to {
		return true
	}
	switch from {
	case schema.Int32:
		return to == schema.Int64 || to == schema.Float64
	case schema.String:
		return to == schema.Bytes
	}
	return false
}

// naiveCanRead 逐字段线性扫描的朴素 CanRead。
func naiveCanRead(R, W []nField) *compat.Violation {
	for _, rf := range R {
		var wf *nField
		for i := range W {
			if W[i].name == rf.name {
				wf = &W[i]
				break
			}
		}
		if wf == nil {
			if !rf.hasDefault {
				return &compat.Violation{Field: rf.name, Reason: compat.MissingNoDefault}
			}
			continue
		}
		if !naivePromotable(wf.typ, rf.typ) {
			return &compat.Violation{Field: rf.name, Reason: compat.TypeMismatch}
		}
		if !wf.required && rf.required && !rf.hasDefault {
			return &compat.Violation{Field: rf.name, Reason: compat.OptionalToRequired}
		}
	}
	return nil
}

func (n *naive) checkClock(now int64) error {
	if n.hasNow && now < n.maxNow {
		return fmt.Errorf("%w: now=%d max=%d", gate.ErrClockRegression, now, n.maxNow)
	}
	return nil
}

func (n *naive) accept(now int64) {
	if !n.hasNow || now > n.maxNow {
		n.maxNow, n.hasNow = now, true
	}
}

func naiveValidNow(now int64) bool {
	return now >= 0 && now <= schema.MaxNow
}

func naiveValidateFields(fields []nField) error {
	if len(fields) == 0 || len(fields) > schema.MaxFields {
		return gate.ErrInvalid
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if f.name == "" || !f.typ.Valid() || seen[f.name] {
			return gate.ErrInvalid
		}
		seen[f.name] = true
	}
	return nil
}

func naiveValidateNames(fields []string) error {
	if len(fields) == 0 || len(fields) > schema.MaxFields {
		return gate.ErrInvalid
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f] {
			return gate.ErrInvalid
		}
		seen[f] = true
	}
	return nil
}

func naiveView(ver []nField, fields []string) ([]nField, error) {
	have := map[string]bool{}
	for _, f := range ver {
		have[f.name] = true
	}
	for _, name := range fields {
		if !have[name] {
			return nil, fmt.Errorf("%w: %q", gate.ErrFieldNotFound, name)
		}
	}
	want := map[string]bool{}
	for _, name := range fields {
		want[name] = true
	}
	var view []nField
	for _, f := range ver {
		if want[f.name] {
			view = append(view, f)
		}
	}
	return view, nil
}

func naiveSameFields(a, b []nField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (n *naive) createSubject(name string, mode schema.Mode, fields []nField, now int64) (int, error) {
	if name == "" || !mode.Valid() || !naiveValidNow(now) {
		return 0, gate.ErrInvalid
	}
	if err := naiveValidateFields(fields); err != nil {
		return 0, err
	}
	if err := n.checkClock(now); err != nil {
		return 0, err
	}
	if _, ok := n.subjects[name]; ok {
		return 0, gate.ErrAlreadyExists
	}
	n.subjects[name] = &nSubject{mode: mode, versions: [][]nField{fields}, subs: map[string]*nSub{}}
	n.accept(now)
	return 1, nil
}

func (n *naive) subscribe(subj, consumer string, pinned int, fields []string, now int64) error {
	if consumer == "" || !naiveValidNow(now) {
		return gate.ErrInvalid
	}
	if err := naiveValidateNames(fields); err != nil {
		return err
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	s, ok := n.subjects[subj]
	if !ok {
		return gate.ErrSubjectNotFound
	}
	if _, ok := s.subs[consumer]; ok {
		return gate.ErrAlreadyExists
	}
	if pinned < 1 || pinned > len(s.versions) {
		return gate.ErrVersionNotFound
	}
	view, err := naiveView(s.versions[pinned-1], fields)
	if err != nil {
		return err
	}
	if v := naiveCanRead(view, s.versions[len(s.versions)-1]); v != nil {
		return &gate.StillBrokenError{Violation: *v}
	}
	s.subs[consumer] = &nSub{pinned: pinned, fields: append([]string(nil), fields...)}
	n.accept(now)
	return nil
}

func (n *naive) waive(subj, consumer string, until, now int64) error {
	if consumer == "" || !naiveValidNow(now) || until <= now {
		return gate.ErrInvalid
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	s, ok := n.subjects[subj]
	if !ok {
		return gate.ErrSubjectNotFound
	}
	sub, ok := s.subs[consumer]
	if !ok {
		return gate.ErrSubscriberNotFound
	}
	sub.hasWaiver, sub.until = true, until
	n.accept(now)
	return nil
}

func (n *naive) publish(subj string, fields []nField, now int64) (int, error) {
	if !naiveValidNow(now) {
		return 0, gate.ErrInvalid
	}
	if err := naiveValidateFields(fields); err != nil {
		return 0, err
	}
	if err := n.checkClock(now); err != nil {
		return 0, err
	}
	s, ok := n.subjects[subj]
	if !ok {
		return 0, gate.ErrSubjectNotFound
	}
	latest := s.versions[len(s.versions)-1]
	if naiveSameFields(latest, fields) {
		return 0, gate.ErrNoChange
	}
	if s.mode == schema.Backward || s.mode == schema.Full {
		if v := naiveCanRead(fields, latest); v != nil {
			return 0, &gate.IncompatibleError{Direction: schema.Backward, Violation: *v}
		}
	}
	if s.mode == schema.Forward || s.mode == schema.Full {
		if v := naiveCanRead(latest, fields); v != nil {
			return 0, &gate.IncompatibleError{Direction: schema.Forward, Violation: *v}
		}
	}
	names := make([]string, 0, len(s.subs))
	for name := range s.subs {
		names = append(names, name)
	}
	sort.Strings(names)
	var blockers []gate.Blocker
	var waived []*nSub
	for _, name := range names {
		sub := s.subs[name]
		if sub.lagging {
			continue
		}
		view, err := naiveView(s.versions[sub.pinned-1], sub.fields)
		if err != nil {
			return 0, err
		}
		v := naiveCanRead(view, fields)
		if v == nil {
			continue
		}
		if sub.hasWaiver && now < sub.until {
			waived = append(waived, sub)
			continue
		}
		blockers = append(blockers, gate.Blocker{Consumer: name, Violation: *v})
	}
	if len(blockers) > 0 {
		return 0, &gate.BlockedError{Blockers: blockers}
	}
	ver := len(s.versions) + 1
	s.versions = append(s.versions, fields)
	for _, sub := range waived {
		sub.lagging, sub.laggingAt = true, ver
	}
	n.accept(now)
	return ver, nil
}

func (n *naive) advance(subj, consumer string, to int, fields []string, now int64) error {
	if consumer == "" || !naiveValidNow(now) {
		return gate.ErrInvalid
	}
	if err := naiveValidateNames(fields); err != nil {
		return err
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	s, ok := n.subjects[subj]
	if !ok {
		return gate.ErrSubjectNotFound
	}
	sub, ok := s.subs[consumer]
	if !ok {
		return gate.ErrSubscriberNotFound
	}
	if to <= sub.pinned || to > len(s.versions) {
		return gate.ErrVersionNotFound
	}
	view, err := naiveView(s.versions[to-1], fields)
	if err != nil {
		return err
	}
	if v := naiveCanRead(view, s.versions[len(s.versions)-1]); v != nil {
		return &gate.StillBrokenError{Violation: *v}
	}
	sub.pinned, sub.fields = to, append([]string(nil), fields...)
	sub.lagging, sub.laggingAt = false, 0
	sub.hasWaiver, sub.until = false, 0
	n.accept(now)
	return nil
}

func (n *naive) status(subj, consumer string) (gate.Status, error) {
	s, ok := n.subjects[subj]
	if !ok {
		return gate.Status{}, gate.ErrSubjectNotFound
	}
	sub, ok := s.subs[consumer]
	if !ok {
		return gate.Status{}, gate.ErrSubscriberNotFound
	}
	return gate.Status{Pinned: sub.pinned, Lagging: sub.lagging, LaggingVersion: sub.laggingAt}, nil
}

// sameErr 比较两个错误是否同类（sentinel）且携带相同判定依据。
func sameErr(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	sentinels := []error{
		gate.ErrInvalid, gate.ErrClockRegression, gate.ErrSubjectNotFound,
		gate.ErrAlreadyExists, gate.ErrSubscriberNotFound, gate.ErrVersionNotFound,
		gate.ErrFieldNotFound, gate.ErrNoChange, gate.ErrIncompatible,
		gate.ErrBlocked, gate.ErrStillBroken,
	}
	for _, s := range sentinels {
		if errors.Is(want, s) != errors.Is(got, s) {
			return false
		}
	}
	var wb, gb *gate.BlockedError
	if errors.As(want, &wb) {
		if !errors.As(got, &gb) || !reflect.DeepEqual(gb.Blockers, wb.Blockers) {
			return false
		}
	}
	var wi, gi *gate.IncompatibleError
	if errors.As(want, &wi) {
		if !errors.As(got, &gi) || *gi != *wi {
			return false
		}
	}
	var ws, gs *gate.StillBrokenError
	if errors.As(want, &ws) {
		if !errors.As(got, &gs) || *gs != *ws {
			return false
		}
	}
	return true
}

func toNFields(fs []schema.Field) []nField {
	out := make([]nField, len(fs))
	for i, f := range fs {
		out[i] = nField{name: f.Name, typ: f.Type, required: f.Required, hasDefault: f.HasDefault}
	}
	return out
}

func fmtFields(fs []schema.Field) string {
	var b strings.Builder
	b.WriteString("[")
	for i, f := range fs {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s:%v(req=%v,def=%v)", f.Name, f.Type, f.Required, f.HasDefault)
	}
	b.WriteString("]")
	return b.String()
}

// checkState 对比两侧全部可观测状态，并验证不变量：
// 任一时刻每个非 Lagging 订阅者的读者视图都能读 latest。
func checkState(t *testing.T, g *gate.Gate, n *naive) {
	t.Helper()
	for name, s := range n.subjects {
		latest, err := g.Latest(name)
		if err != nil || latest != len(s.versions) {
			t.Fatalf("Latest(%q) = %d, %v, want %d", name, latest, err, len(s.versions))
		}
		for cname, sub := range s.subs {
			st, err := g.Status(name, cname)
			if err != nil {
				t.Fatalf("Status(%q, %q) = %v", name, cname, err)
			}
			want := gate.Status{Pinned: sub.pinned, Lagging: sub.lagging, LaggingVersion: sub.laggingAt}
			if st != want {
				t.Fatalf("Status(%q, %q) = %+v, want %+v", name, cname, st, want)
			}
			if sub.lagging {
				continue
			}
			view, err := naiveView(s.versions[sub.pinned-1], sub.fields)
			if err != nil {
				t.Fatalf("view of %q/%q: %v", name, cname, err)
			}
			if v := naiveCanRead(view, s.versions[len(s.versions)-1]); v != nil {
				t.Fatalf("invariant broken: %q/%q view cannot read latest: %v", name, cname, v)
			}
		}
	}
}

// TestRandomAgainstNaive 用 1500 组随机操作序列对照朴素模拟，
// 日志打印每步输入、输出与判定依据（错误值内含方向/字段/原因/阻塞者）。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seed := int64(0); seed < sequences; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runSequence(t, seed)
		})
	}
}

// seqGen 是状态感知的随机操作生成器：偏向合法操作以深入有趣路径，
// 同时保留少量非法输入覆盖拒绝路径。
type seqGen struct {
	r      *rand.Rand
	n      *naive
	cursor int64
}

var (
	seqSubjects  = []string{"s0", "s1", "s2", "sx"}
	seqConsumers = []string{"c0", "c1", "c2", "c3", "cx"}
	seqFields    = []string{"a", "b", "c", "d", "e", "f"}
)

func (g *seqGen) strs(xs []string) string {
	return xs[g.r.Intn(len(xs))]
}

func (g *seqGen) nextNow() int64 {
	switch g.r.Intn(20) {
	case 0:
		return g.cursor - int64(g.r.Intn(20)) - 1 // 回退（可能为负 → 非法）
	case 1:
		return schema.MaxNow + 1 // 越界 → 非法
	default:
		g.cursor += int64(g.r.Intn(4))
		return g.cursor
	}
}

// pickSubject 90% 选已存在的主题，否则从名字池取（可能不存在）。
func (g *seqGen) pickSubject() string {
	if len(g.n.subjects) > 0 && g.r.Intn(10) > 0 {
		names := make([]string, 0, len(g.n.subjects))
		for name := range g.n.subjects {
			names = append(names, name)
		}
		sort.Strings(names)
		return names[g.r.Intn(len(names))]
	}
	return g.strs(seqSubjects)
}

// pickConsumer 70% 选该主题已存在的订阅者。
func (g *seqGen) pickConsumer(subj string) string {
	if s, ok := g.n.subjects[subj]; ok && len(s.subs) > 0 && g.r.Intn(10) < 7 {
		names := make([]string, 0, len(s.subs))
		for name := range s.subs {
			names = append(names, name)
		}
		sort.Strings(names)
		return names[g.r.Intn(len(names))]
	}
	return g.strs(seqConsumers)
}

func (g *seqGen) validFields() []schema.Field {
	k := 1 + g.r.Intn(4)
	perm := g.r.Perm(len(seqFields))
	out := make([]schema.Field, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, schema.Field{
			Name:       seqFields[perm[i]],
			Type:       schema.Type(g.r.Intn(5)),
			Required:   g.r.Intn(2) == 0,
			HasDefault: g.r.Intn(2) == 0,
		})
	}
	return out
}

func (g *seqGen) mkFields() []schema.Field {
	out := g.validFields()
	switch g.r.Intn(20) {
	case 0:
		out = append(out, out[0]) // 重名 → 非法
	case 1:
		out[0].Type = schema.Type(99) // 未知类型 → 非法
	}
	return out
}

func (g *seqGen) mkNames() []string {
	k := 1 + g.r.Intn(3)
	perm := g.r.Perm(len(seqFields))
	out := make([]string, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, seqFields[perm[i]])
	}
	switch g.r.Intn(20) {
	case 0:
		out = append(out, out[0]) // 重复 → 非法
	case 1:
		out = nil // 空 → 非法
	}
	return out
}

// subsetOf 从版本字段中取 1..k 个名字（偶尔重复 → 非法）。
func (g *seqGen) subsetOf(ver []nField) []string {
	k := 1 + g.r.Intn(len(ver))
	perm := g.r.Perm(len(ver))
	out := make([]string, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, ver[perm[i]].name)
	}
	if g.r.Intn(20) == 0 {
		out = append(out, out[0])
	}
	return out
}

// mutate 由 latest 派生候选：改型、删字段、加字段、翻转标志或不变。
func (g *seqGen) mutate(base []nField) []schema.Field {
	out := make([]schema.Field, len(base))
	for i, f := range base {
		out[i] = schema.Field{Name: f.name, Type: f.typ, Required: f.required, HasDefault: f.hasDefault}
	}
	switch g.r.Intn(5) {
	case 0:
		out[g.r.Intn(len(out))].Type = schema.Type(g.r.Intn(5))
	case 1:
		if len(out) > 1 {
			i := g.r.Intn(len(out))
			out = append(out[:i], out[i+1:]...)
		}
	case 2:
		for _, cand := range seqFields {
			used := false
			for _, f := range out {
				if f.Name == cand {
					used = true
					break
				}
			}
			if !used {
				out = append(out, schema.Field{
					Name: cand, Type: schema.Type(g.r.Intn(5)),
					Required: g.r.Intn(2) == 0, HasDefault: g.r.Intn(2) == 0,
				})
				break
			}
		}
	case 3:
		i := g.r.Intn(len(out))
		out[i].Required = !out[i].Required
		out[i].HasDefault = g.r.Intn(2) == 0
	case 4: // 不变 → 触发 ErrNoChange
	}
	return out
}

func runSequence(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	g := gate.New()
	n := newNaive()
	gen := &seqGen{r: r, n: n}

	// 先建三个主题，保证后续操作大多落在已存在主题上。
	for _, subj := range []string{"s0", "s1", "s2"} {
		mode := schema.Mode(r.Intn(3))
		fs := gen.validFields()
		ts := gen.nextNow()
		gotVer, gotErr := g.CreateSubject(subj, mode, fs, ts)
		wantVer, wantErr := n.createSubject(subj, mode, toNFields(fs), ts)
		if !sameErr(gotErr, wantErr) || gotVer != wantVer {
			t.Fatalf("setup CreateSubject(%q): got %d, %v; want %d, %v",
				subj, gotVer, gotErr, wantVer, wantErr)
		}
		t.Logf("setup CreateSubject(%q, %v, %s, now=%d) => ver=%d err=%v",
			subj, mode, fmtFields(fs), ts, gotVer, gotErr)
	}
	checkState(t, g, n)

	for step := 0; step < 40; step++ {
		subj := gen.pickSubject()
		ts := gen.nextNow()
		var gotErr, wantErr error
		var gotVer, wantVer int
		var gotSt, wantSt gate.Status
		hasSt := false
		var desc string
		switch r.Intn(7) {
		case 0: // CreateSubject（多为重复主题或非法参数）
			mode := schema.Mode(r.Intn(4))
			fs := gen.mkFields()
			desc = fmt.Sprintf("CreateSubject(%q, %v, %s, now=%d)", subj, mode, fmtFields(fs), ts)
			gotVer, gotErr = g.CreateSubject(subj, mode, fs, ts)
			wantVer, wantErr = n.createSubject(subj, mode, toNFields(fs), ts)
		case 1: // Subscribe（消费者从名字池取，字段 80% 取 pinned 版本子集）
			consumer := gen.strs(seqConsumers)
			pinned := r.Intn(3)
			fs := gen.mkNames()
			if s, ok := n.subjects[subj]; ok {
				pinned = 1 + r.Intn(len(s.versions))
				if r.Intn(10) == 0 {
					pinned = r.Intn(len(s.versions) + 2)
				}
				if pinned >= 1 && pinned <= len(s.versions) && r.Intn(10) < 8 {
					fs = gen.subsetOf(s.versions[pinned-1])
				}
			}
			desc = fmt.Sprintf("Subscribe(%q, %q, pinned=%d, %v, now=%d)", subj, consumer, pinned, fs, ts)
			gotErr = g.Subscribe(subj, consumer, pinned, fs, ts)
			wantErr = n.subscribe(subj, consumer, pinned, fs, ts)
		case 2: // Waive
			consumer := gen.pickConsumer(subj)
			until := ts + 1 + int64(r.Intn(3))
			if r.Intn(10) == 0 {
				until = ts - int64(r.Intn(2)) // ≤ now → 非法
			}
			desc = fmt.Sprintf("Waive(%q, %q, until=%d, now=%d)", subj, consumer, until, ts)
			gotErr = g.Waive(subj, consumer, until, ts)
			wantErr = n.waive(subj, consumer, until, ts)
		case 3, 4, 5: // Publish（加权，80% 由 latest 派生）
			var fs []schema.Field
			if s, ok := n.subjects[subj]; ok && r.Intn(5) > 0 {
				fs = gen.mutate(s.versions[len(s.versions)-1])
			} else {
				fs = gen.mkFields()
			}
			desc = fmt.Sprintf("Publish(%q, %s, now=%d)", subj, fmtFields(fs), ts)
			gotVer, gotErr = g.Publish(subj, fs, ts)
			wantVer, wantErr = n.publish(subj, toNFields(fs), ts)
		case 6: // Advance 或 Status
			consumer := gen.pickConsumer(subj)
			if r.Intn(2) == 0 {
				to := r.Intn(3)
				fs := gen.mkNames()
				if s, ok := n.subjects[subj]; ok {
					if sub, ok := s.subs[consumer]; ok {
						to = sub.pinned
						if sub.pinned < len(s.versions) {
							to = sub.pinned + 1 + r.Intn(len(s.versions)-sub.pinned)
						}
						if r.Intn(10) == 0 {
							to = r.Intn(len(s.versions) + 2)
						}
					}
					if to >= 1 && to <= len(s.versions) && r.Intn(10) < 8 {
						fs = gen.subsetOf(s.versions[to-1])
					}
				}
				desc = fmt.Sprintf("Advance(%q, %q, to=%d, %v, now=%d)", subj, consumer, to, fs, ts)
				gotErr = g.Advance(subj, consumer, to, fs, ts)
				wantErr = n.advance(subj, consumer, to, fs, ts)
			} else {
				desc = fmt.Sprintf("Status(%q, %q)", subj, consumer)
				gotSt, gotErr = g.Status(subj, consumer)
				wantSt, wantErr = n.status(subj, consumer)
				hasSt = true
			}
		}
		if !sameErr(gotErr, wantErr) || gotVer != wantVer || (hasSt && gotSt != wantSt) {
			t.Fatalf("step %d: %s\ngot:  ver=%d st=%+v err=%v\nwant: ver=%d st=%+v err=%v",
				step, desc, gotVer, gotSt, gotErr, wantVer, wantSt, wantErr)
		}
		t.Logf("step %02d %s => ver=%d err=%v", step, desc, gotVer, gotErr)
		checkState(t, g, n)
	}
}
