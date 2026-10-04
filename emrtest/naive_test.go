package emrtest_test

// 朴素模拟器：按题目规则逐步维护参考状态，不与被测实现共享任何代码。

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
)

type refUser struct {
	dept  string
	level int
	roles int
}

type refVer struct {
	ts      int64
	author  string
	content []byte
	hash    []byte
}

type refAmend struct {
	ts      int64
	user    string
	content []byte
	hash    []byte
}

type refDoc struct {
	enc      string
	author   string
	versions []refVer
	state    int // 0 草稿 1 已签 2 已审签
	sealed   bool
	gen      int64
	sealHash []byte
	window   int64 // >0 处于解封窗口
	defect   bool
	amends   []refAmend
	lateSign []string
}

type refModel struct {
	T, U   int64
	users  map[string]refUser
	encs   map[string]int64 // 就诊 -> 出院时刻；-1 表示未出院
	docs   map[string]*refDoc
	nowMax int64
	hasNow bool
}

func newRef(T, U int64) *refModel {
	return &refModel{T: T, U: U, users: map[string]refUser{},
		encs: map[string]int64{}, docs: map[string]*refDoc{}}
}

func chainHash(prev []byte, ts int64, author string, content []byte) []byte {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(author)))
	var tb [8]byte
	binary.BigEndian.PutUint64(tb[:], uint64(ts))
	h := sha256.New()
	h.Write(prev)
	h.Write(tb[:])
	h.Write(l[:])
	h.Write([]byte(author))
	h.Write(content)
	return h.Sum(nil)
}

func sealHashOf(last []byte, gen int64) []byte {
	var g [8]byte
	binary.BigEndian.PutUint64(g[:], uint64(gen))
	h := sha256.New()
	h.Write(last)
	h.Write(g[:])
	return h.Sum(nil)
}

// tick 在操作开头落地全部到期封存。
func (m *refModel) tick(now int64) []string {
	type ev struct {
		at int64
		id string
	}
	var evs []ev
	for id, d := range m.docs {
		if d.window > 0 {
			if now >= d.window {
				evs = append(evs, ev{d.window, id})
			}
			continue
		}
		dis := m.encs[d.enc]
		if !d.sealed && dis >= 0 && now >= dis+m.T {
			evs = append(evs, ev{dis + m.T, id})
		}
	}
	for i := 1; i < len(evs); i++ {
		for j := i; j > 0; j-- {
			a, b := evs[j-1], evs[j]
			if a.at > b.at || (a.at == b.at && a.id > b.id) {
				evs[j-1], evs[j] = b, a
			} else {
				break
			}
		}
	}
	var done []string
	for _, e := range evs {
		d := m.docs[e.id]
		d.window = 0
		d.sealed = true
		d.gen++
		d.sealHash = sealHashOf(d.versions[len(d.versions)-1].hash, d.gen)
		if d.state != 2 {
			d.defect = true
		}
		done = append(done, e.id)
	}
	return done
}

func (m *refModel) effSealed(d *refDoc) bool { return d.sealed && d.window == 0 }

func (m *refModel) advance(now int64) {
	if !m.hasNow || now > m.nowMax {
		m.nowMax = now
		m.hasNow = true
	}
}

func (m *refModel) addUser(name, dept string, level, roles int) string {
	if name == "" || dept == "" || level < 1 || level > 3 ||
		roles < 0 || roles&^(1|2|4) != 0 {
		return "invalid"
	}
	if _, ok := m.users[name]; ok {
		return "invalid"
	}
	m.users[name] = refUser{dept, level, roles}
	return "ok"
}

func (m *refModel) openEnc(enc string) string {
	if enc == "" {
		return "invalid"
	}
	if _, ok := m.encs[enc]; ok {
		return "invalid"
	}
	m.encs[enc] = -1
	return "ok"
}

func (m *refModel) discharge(now int64, enc string) string {
	if now < 0 || now > 1e9 || enc == "" {
		return "invalid"
	}
	if m.hasNow && now < m.nowMax {
		return "clock"
	}
	dis, ok := m.encs[enc]
	if !ok {
		return "missing"
	}
	if dis >= 0 {
		return "state"
	}
	m.encs[enc] = now
	m.advance(now)
	return "ok"
}

func (m *refModel) pre(now int64, user, doc string) (string, *refDoc, refUser) {
	if now < 0 || now > 1e9 || user == "" || doc == "" {
		return "invalid", nil, refUser{}
	}
	if m.hasNow && now < m.nowMax {
		return "clock", nil, refUser{}
	}
	d, ok := m.docs[doc]
	if !ok {
		return "missing", nil, refUser{}
	}
	u, ok := m.users[user]
	if !ok {
		return "missing", nil, refUser{}
	}
	return "", d, u
}

func (m *refModel) create(now int64, user, enc, doc, content string) string {
	if now < 0 || now > 1e9 || user == "" || enc == "" || doc == "" {
		return "invalid"
	}
	if m.hasNow && now < m.nowMax {
		return "clock"
	}
	dis, ok := m.encs[enc]
	if !ok {
		return "missing"
	}
	if _, ok := m.users[user]; !ok {
		return "missing"
	}
	if _, ok := m.docs[doc]; ok {
		return "missing"
	}
	m.tick(now)
	if dis >= 0 && now >= dis+m.T {
		return "state"
	}
	h := chainHash(make([]byte, 32), now, user, []byte(content))
	m.docs[doc] = &refDoc{enc: enc, author: user,
		versions: []refVer{{ts: now, author: user, content: []byte(content), hash: h}}}
	m.advance(now)
	return "ok"
}

func (m *refModel) edit(now int64, user, doc, content string) string {
	rsn, d, _ := m.pre(now, user, doc)
	if rsn != "" {
		return rsn
	}
	if user != d.author {
		return "perm"
	}
	m.tick(now)
	d = m.docs[doc]
	if m.effSealed(d) || d.state == 2 {
		return "state"
	}
	last := d.versions[len(d.versions)-1].hash
	d.versions = append(d.versions, refVer{ts: now, author: user, content: []byte(content),
		hash: chainHash(last, now, user, []byte(content))})
	if d.state == 1 {
		d.state = 0
	}
	m.advance(now)
	return "ok"
}

func (m *refModel) sign(now int64, user, doc string) string {
	rsn, d, u := m.pre(now, user, doc)
	if rsn != "" {
		return rsn
	}
	if user != d.author {
		return "perm"
	}
	m.tick(now)
	d = m.docs[doc]
	if d.state != 0 {
		return "state"
	}
	late := m.effSealed(d)
	if u.level >= 2 {
		d.state = 2
		if late {
			d.defect = false
		}
	} else {
		d.state = 1
	}
	if late {
		d.lateSign = append(d.lateSign, user+":sign")
	}
	m.advance(now)
	return "ok"
}

func (m *refModel) cosign(now int64, user, doc string) string {
	rsn, d, u := m.pre(now, user, doc)
	if rsn != "" {
		return rsn
	}
	a := m.users[d.author]
	if user == d.author || u.dept != a.dept || u.level < 2 {
		return "perm"
	}
	m.tick(now)
	d = m.docs[doc]
	if d.state != 1 {
		return "state"
	}
	d.state = 2
	if m.effSealed(d) {
		d.defect = false
		d.lateSign = append(d.lateSign, user+":cosign")
	}
	m.advance(now)
	return "ok"
}

func (m *refModel) ret(now int64, user, doc string) string {
	rsn, d, u := m.pre(now, user, doc)
	if rsn != "" {
		return rsn
	}
	a := m.users[d.author]
	if user == d.author || u.dept != a.dept || u.level < 2 {
		return "perm"
	}
	m.tick(now)
	d = m.docs[doc]
	if m.effSealed(d) || d.state != 1 {
		return "state"
	}
	d.state = 0
	m.advance(now)
	return "ok"
}

func (m *refModel) seal(now int64, user, doc string) string {
	rsn, d, u := m.pre(now, user, doc)
	if rsn != "" {
		return rsn
	}
	if u.roles&1 == 0 {
		return "perm"
	}
	m.tick(now)
	d = m.docs[doc]
	if m.effSealed(d) || d.state != 2 {
		return "state"
	}
	d.sealed = true
	d.gen++
	d.sealHash = sealHashOf(d.versions[len(d.versions)-1].hash, d.gen)
	m.advance(now)
	return "ok"
}

func (m *refModel) amend(now int64, user, doc, content string) string {
	rsn, d, u := m.pre(now, user, doc)
	if rsn != "" {
		return rsn
	}
	a := m.users[d.author]
	if user != d.author && (u.dept != a.dept || u.level < 2) {
		return "perm"
	}
	m.tick(now)
	d = m.docs[doc]
	if !m.effSealed(d) {
		return "state"
	}
	var prev []byte
	if n := len(d.amends); n > 0 {
		prev = d.amends[n-1].hash
	} else {
		prev = make([]byte, 32)
	}
	d.amends = append(d.amends, refAmend{ts: now, user: user, content: []byte(content),
		hash: chainHash(prev, now, user, []byte(content))})
	m.advance(now)
	return "ok"
}

func (m *refModel) unseal(now int64, aa, bb, doc string) string {
	if now < 0 || now > 1e9 || aa == "" || bb == "" || doc == "" {
		return "invalid"
	}
	if m.hasNow && now < m.nowMax {
		return "clock"
	}
	d, ok := m.docs[doc]
	if !ok {
		return "missing"
	}
	ua, oka := m.users[aa]
	ub, okb := m.users[bb]
	if !oka || !okb {
		return "missing"
	}
	if aa == bb || ua.roles&2 == 0 || ub.roles&4 == 0 {
		return "perm"
	}
	m.tick(now)
	d = m.docs[doc]
	if !m.effSealed(d) {
		return "state"
	}
	d.window = now + m.U
	m.advance(now)
	return "ok"
}

func (m *refModel) defects() []string {
	var out []string
	for id, d := range m.docs {
		if m.effSealed(d) && d.state != 2 {
			out = append(out, id)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func (m *refModel) verify(doc string) (vOK, aOK bool, seal []byte, found bool) {
	d, ok := m.docs[doc]
	if !ok {
		return false, false, nil, false
	}
	prev := make([]byte, 32)
	vOK = true
	for _, v := range d.versions {
		want := chainHash(prev, v.ts, v.author, v.content)
		if !bytes.Equal(want, v.hash) {
			vOK = false
			break
		}
		prev = want
	}
	prev = make([]byte, 32)
	aOK = true
	for _, a := range d.amends {
		want := chainHash(prev, a.ts, a.user, a.content)
		if !bytes.Equal(want, a.hash) {
			aOK = false
			break
		}
		prev = want
	}
	if d.sealed {
		seal = sealHashOf(d.versions[len(d.versions)-1].hash, d.gen)
	}
	return vOK, aOK, seal, true
}
