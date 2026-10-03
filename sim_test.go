package store

import "ontology/cond"

// naiveVersion is one stored version in the step-by-step reference model.
type naiveVersion struct {
	size   int64
	etag   string
	mtime  int64
	marker bool
}

// naiveModel reimplements the specification independently.
type naiveModel struct {
	versioned map[string]bool
	quota     map[string]int64
	used      map[string]int64
	seq       map[string]int64
	objects   map[string]map[string]map[int64]naiveVersion
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		versioned: map[string]bool{},
		quota:     map[string]int64{},
		used:      map[string]int64{},
		seq:       map[string]int64{},
		objects:   map[string]map[string]map[int64]naiveVersion{},
	}
}

func (m *naiveModel) keys(tenant string) map[string]map[int64]naiveVersion {
	ks := m.objects[tenant]
	if ks == nil {
		ks = map[string]map[int64]naiveVersion{}
		m.objects[tenant] = ks
	}
	return ks
}

func (m *naiveModel) current(tenant, key string) (naiveVersion, int64, bool) {
	vers := m.keys(tenant)[key]
	if len(vers) == 0 {
		return naiveVersion{}, 0, false
	}
	best := int64(-1)
	for n := range vers {
		if n > best {
			best = n
		}
	}
	return vers[best], best, true
}

func (m *naiveModel) view(tenant, key string) cond.View {
	v, _, ok := m.current(tenant, key)
	if !ok {
		return cond.View{}
	}
	return cond.View{Exists: true, Marker: v.marker, Etag: v.etag, Mtime: v.mtime}
}

type simOutcome struct {
	kind string
	ver  int64
	d    int64
}

func simCondValid(c cond.Cond) bool {
	return c.IfUnmodifiedSince == nil ||
		(*c.IfUnmodifiedSince >= 0 && *c.IfUnmodifiedSince <= 1_000_000_000_000)
}

func (m *naiveModel) setQuota(tenant string, q int64) simOutcome {
	if tenant == "" || q < 0 || q > 1_000_000_000_000_000 {
		return simOutcome{kind: "invalid"}
	}
	m.quota[tenant] = q
	return simOutcome{kind: "ok"}
}

func (m *naiveModel) setVersioning(tenant string, on bool) simOutcome {
	if tenant == "" {
		return simOutcome{kind: "invalid"}
	}
	if on {
		m.versioned[tenant] = true
		return simOutcome{kind: "ok"}
	}
	if m.versioned[tenant] {
		return simOutcome{kind: "versioning-immutable"}
	}
	return simOutcome{kind: "ok"}
}

func (m *naiveModel) put(tenant, key string, size int64, etag string, c cond.Cond, now int64, failStorage bool) simOutcome {
	if tenant == "" || key == "" || size < 0 || size > 1_000_000_000_000 ||
		etag == "" || now < 0 || now > 1_000_000_000_000 || !simCondValid(c) {
		return simOutcome{kind: "invalid"}
	}
	if f := c.Evaluate(m.view(tenant, key)); f != "" {
		return simOutcome{kind: "precond:" + string(f)}
	}
	var d int64
	if !m.versioned[tenant] {
		if old, _, ok := m.current(tenant, key); ok {
			d = size - old.size
		} else {
			d = size
		}
	} else {
		d = size
	}
	if d > 0 && m.used[tenant]+d > m.quota[tenant] {
		return simOutcome{kind: "quota"}
	}
	if failStorage {
		return simOutcome{kind: "storage"}
	}
	vers := m.keys(tenant)
	if vers[key] == nil {
		vers[key] = map[int64]naiveVersion{}
	}
	if !m.versioned[tenant] {
		for n := range vers[key] {
			m.used[tenant] -= vers[key][n].size
			delete(vers[key], n)
		}
		vers[key][0] = naiveVersion{size: size, etag: etag, mtime: now}
		m.used[tenant] += size
		return simOutcome{kind: "ok", ver: 0, d: d}
	}
	m.seq[tenant]++
	n := m.seq[tenant]
	vers[key][n] = naiveVersion{size: size, etag: etag, mtime: now}
	m.used[tenant] += size
	return simOutcome{kind: "ok", ver: n, d: d}
}

func (m *naiveModel) del(tenant, key string, ver int64, c cond.Cond, now int64, failStorage bool) simOutcome {
	if tenant == "" || key == "" || ver < -1 || now < 0 || now > 1_000_000_000_000 || !simCondValid(c) {
		return simOutcome{kind: "invalid"}
	}
	if !m.versioned[tenant] && ver != -1 {
		return simOutcome{kind: "invalid"}
	}
	if f := c.Evaluate(m.view(tenant, key)); f != "" {
		return simOutcome{kind: "precond:" + string(f)}
	}
	vers := m.keys(tenant)
	if m.versioned[tenant] {
		if ver != -1 {
			v, ok := vers[key][ver]
			if !ok {
				return simOutcome{kind: "notfound"}
			}
			d := -v.size
			if failStorage {
				return simOutcome{kind: "storage"}
			}
			delete(vers[key], ver)
			m.used[tenant] += d
			return simOutcome{kind: "ok", ver: ver, d: d}
		}
		if failStorage {
			return simOutcome{kind: "storage"}
		}
		m.seq[tenant]++
		n := m.seq[tenant]
		if vers[key] == nil {
			vers[key] = map[int64]naiveVersion{}
		}
		vers[key][n] = naiveVersion{marker: true, mtime: now}
		return simOutcome{kind: "ok", ver: n, d: 0}
	}
	old, _, ok := m.current(tenant, key)
	if !ok {
		return simOutcome{kind: "notfound"}
	}
	d := -old.size
	if failStorage {
		return simOutcome{kind: "storage"}
	}
	for n := range vers[key] {
		delete(vers[key], n)
	}
	m.used[tenant] += d
	return simOutcome{kind: "ok", ver: 0, d: d}
}
