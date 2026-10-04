package gate_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/compat"
	"ontology/gate"
	"ontology/schema"
)

func TestRejectionOrder(t *testing.T) {
	type tc struct {
		name string
		run  func(*gate.Registry) error
		want error
	}
	cases := []tc{
		{"invalid fields beats all", func(r *gate.Registry) error {
			_, err := r.CreateSubject("zz", schema.Backward, nil, -5)
			return err
		}, gate.ErrInvalidArgument},
		{"invalid mode", func(r *gate.Registry) error {
			_, err := r.CreateSubject("zz", schema.Mode(99), v1Fields(), 0)
			return err
		}, gate.ErrInvalidArgument},
		{"now out of range", func(r *gate.Registry) error {
			_, err := r.Publish("s", candY(), 1_000_000_000_001)
			return err
		}, gate.ErrInvalidArgument},
		{"unknown subject", func(r *gate.Registry) error {
			_, err := r.Publish("nope", candY(), 11)
			return err
		}, gate.ErrNotFound},
		{"clock skew on known subject", func(r *gate.Registry) error {
			_, err := r.Publish("s", candY(), 9)
			return err
		}, gate.ErrClockSkew},
		{"duplicate subject", func(r *gate.Registry) error {
			_, err := r.CreateSubject("s", schema.Backward, v1Fields(), 10)
			return err
		}, gate.ErrExists},
		{"duplicate subscriber", func(r *gate.Registry) error {
			return r.Subscribe("s", "c1", 1, []string{"id"}, 11)
		}, gate.ErrExists},
		{"unknown subscriber waive", func(r *gate.Registry) error {
			return r.Waive("s", "ghost", 20, 11)
		}, gate.ErrNotFound},
		{"pinned version missing", func(r *gate.Registry) error {
			return r.Subscribe("s", "c9", 99, []string{"id"}, 11)
		}, gate.ErrNotFound},
		{"pinned field missing", func(r *gate.Registry) error {
			return r.Subscribe("s", "c9", 1, []string{"id", "ghost"}, 11)
		}, gate.ErrNotFound},
		{"duplicate fields invalid", func(r *gate.Registry) error {
			return r.Subscribe("s", "c9", 1, []string{"id", "id"}, 11)
		}, gate.ErrInvalidArgument},
		{"still broken subscribe", func(r *gate.Registry) error {
			if v, err := r.Publish("s", candY(), 11); err != nil || v != 2 {
				t.Fatalf("setup publish = %d,%v", v, err)
			}
			return r.Subscribe("s", "c9", 1, []string{"age"}, 12)
		}, gate.ErrStillBroken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := gate.NewRegistry()
			if v, err := r.CreateSubject("s", schema.Backward, v1Fields(), 10); err != nil || v != 1 {
				t.Fatalf("setup create = %d,%v", v, err)
			}
			if err := r.Subscribe("s", "c1", 1, []string{"id"}, 10); err != nil {
				t.Fatalf("setup subscribe: %v", err)
			}
			if err := c.run(r); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestPublishComparedBudget(t *testing.T) {
	for _, history := range []int{10, 1000} {
		t.Run(fmt.Sprintf("history=%d", history), func(t *testing.T) {
			r := gate.NewRegistry()
			base := make([]schema.Field, 0, 60)
			for i := 0; i < 60; i++ {
				base = append(base, gf(fmt.Sprintf("f%02d", i), schema.Int32, true, false))
			}
			if _, err := r.CreateSubject("s", schema.Backward, base, 0); err != nil {
				t.Fatal(err)
			}
			if err := r.Subscribe("s", "a", 1, []string{"f00", "f01"}, 0); err != nil {
				t.Fatal(err)
			}
			if err := r.Subscribe("s", "b", 1, []string{"f02", "f03"}, 0); err != nil {
				t.Fatal(err)
			}
			// Heavy load on another subject must not count against s.
			otherBase := []schema.Field{gf("g0", schema.Int32, true, false)}
			if _, err := r.CreateSubject("other", schema.Backward, otherBase, 0); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 50; i++ {
				if err := r.Subscribe("other", fmt.Sprintf("z%02d", i), 1, []string{"g0"}, 0); err != nil {
					t.Fatal(err)
				}
			}
			for k := 2; k <= history; k++ {
				n := append([]schema.Field{}, base...)
				extraName := "e1"
				if k%2 == 0 {
					extraName = "e2"
				}
				n = append(n, gf(extraName, schema.String, true, true))
				before := compat.Compared()
				v, err := r.Publish("s", n, int64(k))
				delta := compat.Compared() - before
				if err != nil || v != k {
					t.Fatalf("publish %d = %d,%v", k, v, err)
				}
				bound := uint64(len(n) + len(n) + 4)
				if delta > bound {
					t.Fatalf("history=%d publish %d compared delta=%d > bound=%d", history, k, delta, bound)
				}
			}
		})
	}
}

func TestConcurrentSerializable(t *testing.T) {
	r := gate.NewRegistry()
	if _, err := r.CreateSubject("s", schema.Backward, v1Fields(), 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			c := fmt.Sprintf("c%d", g)
			_ = r.Subscribe("s", c, 1, []string{"id"}, 0)
			for i := 0; i < 50; i++ {
				now := int64(1 + i)
				n := []schema.Field{
					gf("id", schema.Int32, true, false),
					gf("name", schema.String, true, i%2 == 0),
				}
				_, _ = r.Publish("s", n, now)
				_ = r.Waive("s", c, now+1, now)
				if st, err := r.Status("s", c); err == nil && !st.Lagging {
					if err := r.Advance("s", c, 1, []string{"id"}, now); err != nil &&
						!errors.Is(err, gate.ErrNotFound) &&
						!errors.Is(err, gate.ErrStillBroken) &&
						!errors.Is(err, gate.ErrClockSkew) {
						t.Errorf("unexpected advance err: %v", err)
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

// --- naive reference model -------------------------------------------------

type mField struct {
	name       string
	typ        schema.Type
	required   bool
	hasDefault bool
}

type mVersion struct{ fields []mField }

type mConsumer struct {
	pinned     int
	projection []string
	lagging    bool
	lagged     int
	until      int64
	hasWaive   bool
}

type mSubject struct {
	mode      schema.Mode
	versions  []mVersion
	consumers map[string]*mConsumer
	now       int64
}

type naiveModel struct {
	subjects map[string]*mSubject
}

type oc struct {
	ver    int
	class  string
	detail string
}

func mFind(v mVersion, name string) (mField, bool) {
	for _, f := range v.fields {
		if f.name == name {
			return f, true
		}
	}
	return mField{}, false
}

func mPromotable(from, to schema.Type) bool {
	if from == to {
		return true
	}
	switch {
	case to == schema.Int64 && from == schema.Int32:
		return true
	case to == schema.Float64 && from == schema.Int32:
		return true
	case to == schema.Bytes && from == schema.String:
		return true
	}
	return false
}

func mCanRead(view []mField, writer mVersion) (string, compat.Reason, bool) {
	for _, rf := range view {
		wf, ok := mFind(writer, rf.name)
		if !ok {
			if !rf.hasDefault {
				return rf.name, compat.MissingNoDefault, false
			}
			continue
		}
		if !mPromotable(wf.typ, rf.typ) {
			return rf.name, compat.TypeMismatch, false
		}
		if !wf.required && rf.required && !rf.hasDefault {
			return rf.name, compat.OptionalToRequired, false
		}
	}
	return "", 0, true
}

func mProject(v mVersion, names []string) []mField {
	out := []mField{}
	for _, f := range v.fields {
		for _, n := range names {
			if f.name == n {
				out = append(out, f)
			}
		}
	}
	return out
}

func mValidFields(fs []mField) bool {
	if len(fs) < 1 || len(fs) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, f := range fs {
		if f.name == "" || !schema.ValidType(f.typ) || seen[f.name] {
			return false
		}
		seen[f.name] = true
	}
	return true
}

func mValidNames(names []string) bool {
	if len(names) < 1 || len(names) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			return false
		}
		seen[n] = true
	}
	return true
}

func toM(fs []schema.Field) []mField {
	out := make([]mField, len(fs))
	for i, f := range fs {
		out[i] = mField{f.Name, f.Type, f.Required, f.HasDefault}
	}
	return out
}

func mEqual(a, b []mField) bool {
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

func newNaive() *naiveModel {
	return &naiveModel{subjects: map[string]*mSubject{}}
}

const maxT = int64(1_000_000_000_000)

func (m *naiveModel) create(name string, mode schema.Mode, fs []schema.Field, now int64) oc {
	nf := toM(fs)
	if !mValidFields(nf) || !schema.ValidMode(mode) || now < 0 || now > maxT {
		return oc{class: "invalid"}
	}
	if _, ok := m.subjects[name]; ok {
		return oc{class: "exists"}
	}
	m.subjects[name] = &mSubject{
		mode:      mode,
		versions:  []mVersion{{fields: nf}},
		consumers: map[string]*mConsumer{},
		now:       now,
	}
	return oc{ver: 1, class: "ok"}
}

func (m *naiveModel) subscribe(sn, cn string, pinned int, fields []string, now int64) oc {
	if cn == "" || !mValidNames(fields) || now < 0 || now > maxT {
		return oc{class: "invalid"}
	}
	s, ok := m.subjects[sn]
	if !ok {
		return oc{class: "notfound", detail: "subject"}
	}
	if now < s.now {
		return oc{class: "skew"}
	}
	if _, dup := s.consumers[cn]; dup {
		return oc{class: "exists"}
	}
	if pinned < 1 || pinned > len(s.versions) {
		return oc{class: "notfound", detail: "version"}
	}
	v := s.versions[pinned-1]
	for _, n := range fields {
		if _, ok := mFind(v, n); !ok {
			return oc{class: "notfound", detail: "field"}
		}
	}
	view := mProject(v, fields)
	latest := s.versions[len(s.versions)-1]
	if fld, reason, ok := mCanRead(view, latest); !ok {
		return oc{class: "broken", detail: fmt.Sprintf("%s:%d", fld, reason)}
	}
	s.consumers[cn] = &mConsumer{pinned: pinned, projection: append([]string{}, fields...)}
	s.now = now
	return oc{class: "ok"}
}

func (m *naiveModel) waive(sn, cn string, until, now int64) oc {
	if now < 0 || now > maxT || until <= now {
		return oc{class: "invalid"}
	}
	s, ok := m.subjects[sn]
	if !ok {
		return oc{class: "notfound", detail: "subject"}
	}
	if now < s.now {
		return oc{class: "skew"}
	}
	c, ok := s.consumers[cn]
	if !ok {
		return oc{class: "notfound", detail: "consumer"}
	}
	c.until = until
	c.hasWaive = true
	s.now = now
	return oc{class: "ok"}
}

func (m *naiveModel) advance(sn, cn string, to int, fields []string, now int64) oc {
	if !mValidNames(fields) || now < 0 || now > maxT {
		return oc{class: "invalid"}
	}
	s, ok := m.subjects[sn]
	if !ok {
		return oc{class: "notfound", detail: "subject"}
	}
	if now < s.now {
		return oc{class: "skew"}
	}
	c, ok := s.consumers[cn]
	if !ok {
		return oc{class: "notfound", detail: "consumer"}
	}
	if to <= c.pinned || to > len(s.versions) {
		return oc{class: "notfound", detail: "to"}
	}
	v := s.versions[to-1]
	for _, n := range fields {
		if _, ok := mFind(v, n); !ok {
			return oc{class: "notfound", detail: "field"}
		}
	}
	view := mProject(v, fields)
	latest := s.versions[len(s.versions)-1]
	if fld, reason, ok := mCanRead(view, latest); !ok {
		return oc{class: "broken", detail: fmt.Sprintf("%s:%d", fld, reason)}
	}
	c.pinned = to
	c.projection = append([]string{}, fields...)
	c.lagging = false
	c.lagged = 0
	c.until = 0
	c.hasWaive = false
	s.now = now
	return oc{class: "ok"}
}

func (m *naiveModel) publish(sn string, fs []schema.Field, now int64) oc {
	nf := toM(fs)
	if !mValidFields(nf) || now < 0 || now > maxT {
		return oc{class: "invalid"}
	}
	s, ok := m.subjects[sn]
	if !ok {
		return oc{class: "notfound", detail: "subject"}
	}
	if now < s.now {
		return oc{class: "skew"}
	}
	latest := s.versions[len(s.versions)-1]
	if mEqual(nf, latest.fields) {
		return oc{class: "nochange"}
	}
	cand := mVersion{fields: nf}
	if s.mode == schema.Backward || s.mode == schema.Full {
		if fld, reason, ok := mCanRead(nf, latest); !ok {
			return oc{class: "incompatible", detail: fmt.Sprintf("BACKWARD:%s:%d", fld, reason)}
		}
	}
	if s.mode == schema.Forward || s.mode == schema.Full {
		if fld, reason, ok := mCanRead(latest.fields, cand); !ok {
			return oc{class: "incompatible", detail: fmt.Sprintf("FORWARD:%s:%d", fld, reason)}
		}
	}
	names := make([]string, 0, len(s.consumers))
	for n := range s.consumers {
		names = append(names, n)
	}
	sortStrings(names)
	type blk struct {
		name   string
		field  string
		reason compat.Reason
	}
	var blockers []blk
	var waived []string
	for _, n := range names {
		c := s.consumers[n]
		if c.lagging {
			continue
		}
		view := mProject(s.versions[c.pinned-1], c.projection)
		fld, reason, ok := mCanRead(view, cand)
		if ok {
			continue
		}
		if c.hasWaive && now < c.until {
			waived = append(waived, n)
			continue
		}
		blockers = append(blockers, blk{n, fld, reason})
	}
	if len(blockers) > 0 {
		parts := make([]string, len(blockers))
		for i, b := range blockers {
			parts[i] = fmt.Sprintf("%s:%s:%d", b.name, b.field, b.reason)
		}
		return oc{class: "blocked", detail: strings.Join(parts, ",")}
	}
	newNum := len(s.versions) + 1
	s.versions = append(s.versions, cand)
	for _, n := range waived {
		c := s.consumers[n]
		c.lagging = true
		c.lagged = newNum
	}
	s.now = now
	return oc{ver: newNum, class: "ok"}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
