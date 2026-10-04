package seal_test

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/record"
	"ontology/seal"
	"ontology/sign"
)

// ---------- 独立朴素模型：严格按题意逐步重写，不引用实现内部 ----------

type nUser struct {
	dept  string
	level int
	roles map[string]bool
}

type nNode struct {
	ts              int64
	author, content string
	hash            []byte
}

type nDoc struct {
	id, enc, author string
	status          int
	vers, amends    []nNode
	sealed          bool
	gen             int
	sealHash        []byte
	winOpen         bool
	winEnd          int64
	signTS          int64
	signLate        bool
	cosignTS        int64
	cosignBy        string
	cosignLate      bool
}

type nEnc struct {
	dis        int64
	discharged bool
}

type naive struct {
	T, U   int64
	users  map[string]nUser
	encs   map[string]*nEnc
	docs   map[string]*nDoc
	maxNow int64
	hashes int
}

var nZero = make([]byte, 32)

func nHash(prev []byte, ts int64, author, content string) []byte {
	h := sha256.New()
	h.Write(prev)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(ts))
	h.Write(b[:])
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len([]byte(author))))
	h.Write(l[:])
	h.Write([]byte(author))
	h.Write([]byte(content))
	return h.Sum(nil)
}

func (n *naive) hash(prev []byte, ts int64, a, c string) []byte {
	n.hashes++
	return nHash(prev, ts, a, c)
}

func (n *naive) sealHash(head []byte, gen int) []byte {
	n.hashes++
	h := sha256.New()
	h.Write(head)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(gen))
	h.Write(b[:])
	return h.Sum(nil)
}

func (n *naive) effSealed(d *nDoc, now int64) bool {
	if !d.sealed {
		return false
	}
	if d.winOpen && now < d.winEnd {
		return false
	}
	return true
}

func (n *naive) doSeal(d *nDoc, now int64) {
	d.sealed = true
	d.winOpen = false
	d.winEnd = 0
	d.gen++
	head := nZero
	if len(d.vers) > 0 {
		head = d.vers[len(d.vers)-1].hash
	}
	d.sealHash = n.sealHash(head, d.gen)
}

func (n *naive) sweep(now int64) []string {
	var due []*nDoc
	for _, d := range n.docs {
		if d.sealed && d.winOpen && now >= d.winEnd {
			due = append(due, d)
			continue
		}
		if !d.sealed {
			e := n.encs[d.enc]
			if e != nil && e.discharged && now >= e.dis+n.T {
				due = append(due, d)
			}
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].id < due[j].id })
	var ids []string
	for _, d := range due {
		n.doSeal(d, now)
		ids = append(ids, d.id)
	}
	return ids
}

func (n *naive) clone() map[string]*nDoc {
	cp := map[string]*nDoc{}
	for id, d := range n.docs {
		c := *d
		c.vers = append([]nNode(nil), d.vers...)
		c.amends = append([]nNode(nil), d.amends...)
		c.sealHash = append([]byte(nil), d.sealHash...)
		cp[id] = &c
	}
	return cp
}

func (n *naive) restore(cp map[string]*nDoc, h0 int) {
	n.docs = cp
	n.hashes = h0
}

const (
	cOK         = "ok"
	cInvalid    = "invalid"
	cClock      = "clock"
	cNotFound   = "notfound"
	cPermission = "permission"
	cState      = "state"
)

type opKind int

const (
	kOpenEnc opKind = iota
	kDischarge
	kCreate
	kEdit
	kSign
	kCosign
	kReturn
	kSeal
	kAmend
	kUnseal
	kDefects
)

type op struct {
	kind              opKind
	now               int64
	user, a, b        string
	enc, doc, content string
}

func (o op) String() string {
	switch o.kind {
	case kOpenEnc:
		return fmt.Sprintf("OpenEnc(enc=%s)", o.enc)
	case kDischarge:
		return fmt.Sprintf("Discharge(now=%d,enc=%s)", o.now, o.enc)
	case kCreate:
		return fmt.Sprintf("Create(now=%d,u=%s,enc=%s,doc=%s,q=%q)", o.now, o.user, o.enc, o.doc, o.content)
	case kEdit:
		return fmt.Sprintf("Edit(now=%d,u=%s,doc=%s,q=%q)", o.now, o.user, o.doc, o.content)
	case kSign:
		return fmt.Sprintf("Sign(now=%d,u=%s,doc=%s)", o.now, o.user, o.doc)
	case kCosign:
		return fmt.Sprintf("Cosign(now=%d,u=%s,doc=%s)", o.now, o.user, o.doc)
	case kReturn:
		return fmt.Sprintf("Return(now=%d,u=%s,doc=%s)", o.now, o.user, o.doc)
	case kSeal:
		return fmt.Sprintf("Seal(now=%d,u=%s,doc=%s)", o.now, o.user, o.doc)
	case kAmend:
		return fmt.Sprintf("Amend(now=%d,u=%s,doc=%s,q=%q)", o.now, o.user, o.doc, o.content)
	case kUnseal:
		return fmt.Sprintf("Unseal(now=%d,a=%s,b=%s,doc=%s)", o.now, o.a, o.b, o.doc)
	default:
		return fmt.Sprintf("Defects(now=%d)", o.now)
	}
}

func validTS(now int64) bool { return now >= 0 && now <= 1_000_000_000 }

// step 按朴素规则执行一步，返回判定类别与依据。
func (n *naive) step(o op) (string, string) {
	needTS := o.kind != kOpenEnc
	nonEmpty := true
	switch o.kind {
	case kDischarge:
		nonEmpty = o.enc != ""
	case kCreate:
		nonEmpty = o.user != "" && o.enc != "" && o.doc != ""
	case kEdit, kSign, kCosign, kReturn, kSeal, kAmend:
		nonEmpty = o.user != "" && o.doc != ""
	case kUnseal:
		nonEmpty = o.a != "" && o.b != "" && o.doc != ""
	}
	if (needTS && !validTS(o.now)) || !nonEmpty {
		return cInvalid, "参数非法"
	}
	if needTS && o.now < n.maxNow {
		return cClock, "时钟回退"
	}

	switch o.kind {
	case kOpenEnc:
		if n.encs[o.enc] != nil {
			return cNotFound, "就诊号已存在"
		}
		n.encs[o.enc] = &nEnc{dis: -1}
		return cOK, "建立就诊"
	case kDischarge:
		e := n.encs[o.enc]
		if e == nil {
			return cNotFound, "就诊不存在"
		}
		if e.discharged {
			return cState, "重复出院"
		}
		e.discharged = true
		e.dis = o.now
		n.maxNow = o.now
		return cOK, fmt.Sprintf("出院时刻=%d", o.now)
	case kDefects:
		var ids []string
		for _, d := range n.docs {
			if n.effSealed(d, o.now) && d.status != record.StatusCosigned {
				ids = append(ids, d.id)
			}
		}
		sort.Strings(ids)
		return cOK, "缺陷=[" + strings.Join(ids, ",") + "]"
	}

	// 存在性
	if o.kind == kCreate {
		if _, ok := n.users[o.user]; !ok {
			return cNotFound, "作者不存在"
		}
		if _, ok := n.encs[o.enc]; !ok {
			return cNotFound, "就诊不存在"
		}
		if n.docs[o.doc] != nil {
			return cNotFound, "文档号已存在"
		}
	} else if o.kind == kUnseal {
		if _, ok := n.users[o.a]; !ok {
			return cNotFound, "用户 a 不存在"
		}
		if _, ok := n.users[o.b]; !ok {
			return cNotFound, "用户 b 不存在"
		}
		if n.docs[o.doc] == nil {
			return cNotFound, "文档不存在"
		}
	} else {
		if _, ok := n.users[o.user]; !ok {
			return cNotFound, "用户不存在"
		}
		if n.docs[o.doc] == nil {
			return cNotFound, "文档不存在"
		}
	}

	d := n.docs[o.doc]

	// 权限（在 sweep 之前）
	switch o.kind {
	case kEdit, kSign:
		if o.user != d.author {
			return cPermission, "非作者"
		}
	case kCosign, kReturn:
		u, au := n.users[o.user], n.users[d.author]
		if o.user == d.author || u.dept != au.dept || u.level < 2 {
			return cPermission, "须同科室 level>=2 且非作者本人"
		}
	case kSeal:
		if !n.users[o.user].roles[record.RoleArchivist] {
			return cPermission, "非归档员"
		}
	case kAmend:
		u, au := n.users[o.user], n.users[d.author]
		if !(o.user == d.author || (u.dept == au.dept && u.level >= 2)) {
			return cPermission, "补记须作者本人或同科室 level>=2"
		}
	case kUnseal:
		ua, ub := n.users[o.a], n.users[o.b]
		if o.a == o.b || !ua.roles[record.RoleMedical] || !ub.roles[record.RoleRecords] {
			return cPermission, "须两个不同的人分别持医务审批与病案审批"
		}
	}

	snap, h0 := n.clone(), n.hashes
	sealedIDs := n.sweep(o.now)
	rollback := func(cat, why string) (string, string) {
		n.restore(snap, h0)
		return cat, why
	}

	switch o.kind {
	case kCreate:
		e := n.encs[o.enc]
		if e.discharged && o.now >= e.dis+n.T {
			return rollback(cState, "已到封存时刻，就诊不得新建文档")
		}
		nd := &nDoc{id: o.doc, enc: o.enc, author: o.user, status: record.StatusDraft}
		n.docs[o.doc] = nd
		nd.vers = append(nd.vers, nNode{ts: o.now, author: o.user, content: o.content, hash: n.hash(nZero, o.now, o.user, o.content)})
		n.maxNow = o.now
		return cOK, "建立 v1；sweep=[" + strings.Join(sealedIDs, ",") + "]"
	case kEdit:
		if n.effSealed(d, o.now) {
			return rollback(cState, "封存中不可改")
		}
		if d.status == record.StatusCosigned {
			return rollback(cState, "已审签不可改")
		}
		prev := nZero
		if len(d.vers) > 0 {
			prev = d.vers[len(d.vers)-1].hash
		}
		d.vers = append(d.vers, nNode{ts: o.now, author: o.user, content: o.content, hash: n.hash(prev, o.now, o.user, o.content)})
		if d.status == record.StatusSigned {
			d.status = record.StatusDraft
			d.signTS, d.signLate = 0, false
			d.cosignTS, d.cosignBy, d.cosignLate = 0, "", false
		}
		n.maxNow = o.now
		return cOK, fmt.Sprintf("追加 v%d（已签则退回草稿）；sweep=[%s]", len(d.vers), strings.Join(sealedIDs, ","))
	case kSign:
		if d.status != record.StatusDraft {
			return rollback(cState, "仅草稿可签")
		}
		late := n.effSealed(d, o.now)
		d.signTS, d.signLate = o.now, late
		if n.users[o.user].level >= 2 {
			d.status = record.StatusCosigned
		} else {
			d.status = record.StatusSigned
		}
		n.maxNow = o.now
		return cOK, fmt.Sprintf("签名 late=%v status=%d；sweep=[%s]", late, d.status, strings.Join(sealedIDs, ","))
	case kCosign:
		if d.status != record.StatusSigned {
			return rollback(cState, "仅已签可审签")
		}
		late := n.effSealed(d, o.now)
		d.status = record.StatusCosigned
		d.cosignTS, d.cosignBy, d.cosignLate = o.now, o.user, late
		n.maxNow = o.now
		return cOK, fmt.Sprintf("审签 late=%v；sweep=[%s]", late, strings.Join(sealedIDs, ","))
	case kReturn:
		if n.effSealed(d, o.now) {
			return rollback(cState, "封存中不可退回")
		}
		if d.status != record.StatusSigned {
			return rollback(cState, "仅已签可退回")
		}
		d.status = record.StatusDraft
		d.signTS, d.signLate = 0, false
		d.cosignTS, d.cosignBy, d.cosignLate = 0, "", false
		n.maxNow = o.now
		return cOK, "退回草稿；sweep=[" + strings.Join(sealedIDs, ",") + "]"
	case kSeal:
		if n.effSealed(d, o.now) || d.winOpen || d.status != record.StatusCosigned {
			return rollback(cState, "手动封存要求未封存且已审签")
		}
		n.doSeal(d, o.now)
		n.maxNow = o.now
		return cOK, "手动封存 gen=" + fmt.Sprint(d.gen)
	case kAmend:
		if !n.effSealed(d, o.now) {
			return rollback(cState, "仅封存后可补记")
		}
		prev := nZero
		if len(d.amends) > 0 {
			prev = d.amends[len(d.amends)-1].hash
		}
		d.amends = append(d.amends, nNode{ts: o.now, author: o.user, content: o.content, hash: n.hash(prev, o.now, o.user, o.content)})
		n.maxNow = o.now
		return cOK, fmt.Sprintf("补记 a%d；sweep=[%s]", len(d.amends), strings.Join(sealedIDs, ","))
	case kUnseal:
		if !n.effSealed(d, o.now) {
			return rollback(cState, "仅封存中的文档可解封")
		}
		d.winOpen = true
		d.winEnd = o.now + n.U
		n.maxNow = o.now
		return cOK, fmt.Sprintf("开启解封窗口至 %d", d.winEnd)
	}
	return cInvalid, "未覆盖"
}

// ---------- 真实系统执行 ----------

func errCat(err error) string {
	switch {
	case err == nil:
		return cOK
	case errorsIs(err, record.ErrInvalid):
		return cInvalid
	case errorsIs(err, record.ErrClock):
		return cClock
	case errorsIs(err, record.ErrNotFound):
		return cNotFound
	case errorsIs(err, record.ErrPermission):
		return cPermission
	default:
		return cState
	}
}

func errorsIs(err, target error) bool { return err != nil && err.Error() == target.Error() }

func runReal(s *record.Store, o op) (string, string) {
	var err error
	switch o.kind {
	case kOpenEnc:
		err = s.OpenEnc(o.enc)
	case kDischarge:
		err = s.Discharge(o.now, o.enc)
	case kCreate:
		err = s.Create(o.now, o.user, o.enc, o.doc, o.content)
	case kEdit:
		err = s.Edit(o.now, o.user, o.doc, o.content)
	case kSign:
		err = sign.Sign(s, o.now, o.user, o.doc)
	case kCosign:
		err = sign.Cosign(s, o.now, o.user, o.doc)
	case kReturn:
		err = sign.Return(s, o.now, o.user, o.doc)
	case kSeal:
		err = seal.Seal(s, o.now, o.user, o.doc)
	case kAmend:
		err = seal.Amend(s, o.now, o.user, o.doc, o.content)
	case kUnseal:
		err = seal.Unseal(s, o.now, o.a, o.b, o.doc)
	case kDefects:
		ids, e := s.Defects(o.now)
		err = e
		if e == nil {
			return cOK, "缺陷=[" + strings.Join(ids, ",") + "]"
		}
	}
	return errCat(err), fmt.Sprintf("err=%v", err)
}

// ---------- 状态对照 ----------

func cmpNode(a []record.Node, b []nNode) string {
	if len(a) != len(b) {
		return fmt.Sprintf("链长度 %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i].TS != b[i].ts || a[i].Author != b[i].author || a[i].Content != b[i].content {
			return fmt.Sprintf("节点[%d] 元数据不一致", i)
		}
		if string(a[i].Hash) != string(b[i].hash) {
			return fmt.Sprintf("节点[%d] 哈希不一致", i)
		}
	}
	return ""
}

func cmpState(t *testing.T, s *record.Store, n *naive, stepNo int, o op) {
	t.Helper()
	ids := s.DocIDs()
	if len(ids) != len(n.docs) {
		t.Fatalf("step %d %s: 文档数 %d != %d", stepNo, o, len(ids), len(n.docs))
	}
	for _, id := range ids {
		sd, _ := s.Snapshot(id)
		nd := n.docs[id]
		if nd == nil {
			t.Fatalf("step %d: 朴素模型缺文档 %s", stepNo, id)
		}
		if sd.Enc != nd.enc || sd.Author != nd.author || sd.Status != nd.status ||
			sd.Sealed != nd.sealed || sd.Gen != nd.gen || sd.WindowOpen != nd.winOpen || sd.UnsealEnd != nd.winEnd ||
			sd.SignTS != nd.signTS || sd.SignLate != nd.signLate ||
			sd.CosignTS != nd.cosignTS || sd.CosignBy != nd.cosignBy || sd.CosignLate != nd.cosignLate {
			t.Fatalf("step %d %s: 文档 %s 状态不一致\n real=%+v\nnaive=%+v", stepNo, o, id, sd, nd)
		}
		if string(sd.SealHash) != string(nd.sealHash) {
			t.Fatalf("step %d %s: 文档 %s sealHash 不一致", stepNo, o, id)
		}
		if msg := cmpNode(sd.Vers, nd.vers); msg != "" {
			t.Fatalf("step %d %s: 文档 %s 版本链 %s", stepNo, o, id, msg)
		}
		if msg := cmpNode(sd.Amends, nd.amends); msg != "" {
			t.Fatalf("step %d %s: 文档 %s 补记链 %s", stepNo, o, id, msg)
		}
	}
	if s.MaxNow() != n.maxNow {
		t.Fatalf("step %d %s: maxNow %d != %d", stepNo, o, s.MaxNow(), n.maxNow)
	}
	if s.Hashes() != n.hashes {
		t.Fatalf("step %d %s: hashes %d != %d", stepNo, o, s.Hashes(), n.hashes)
	}
}

// ---------- 随机序列生成与主测试 ----------

var diffUsers = []struct {
	name, dept string
	level      int
	roles      []string
}{
	{"r1", "内科", 1, nil},
	{"r2", "内科", 1, nil},
	{"s1", "内科", 2, nil},
	{"s2", "内科", 2, []string{record.RoleArchivist}},
	{"k3", "外科", 3, nil},
	{"am", "医务处", 2, []string{record.RoleMedical}},
	{"ba", "病案科", 2, []string{record.RoleRecords}},
}

func initWorld() (*record.Store, *naive) {
	s := record.New(50, 10)
	n := &naive{
		T: 50, U: 10,
		users:  map[string]nUser{},
		encs:   map[string]*nEnc{},
		docs:   map[string]*nDoc{},
		maxNow: -1,
	}
	for _, u := range diffUsers {
		rs := map[string]bool{}
		for _, r := range u.roles {
			rs[r] = true
		}
		if err := s.AddUser(u.name, u.dept, u.level, u.roles); err != nil {
			panic(err)
		}
		n.users[u.name] = nUser{dept: u.dept, level: u.level, roles: rs}
	}
	return s, n
}

func pickUser(rng *rand.Rand) string { return diffUsers[rng.Intn(len(diffUsers))].name }

func genOps(rng *rand.Rand) []op {
	var ops []op
	now := int64(100)
	adv := func() int64 {
		now += int64(rng.Intn(12)) // 小步推进，制造窗口/封存取等
		return now
	}
	docSeq := 0
	docIDs := []string{}
	ops = append(ops, op{kind: kOpenEnc, enc: "e"})
	// 以较大概率安排一次出院
	disAt := now + int64(20+rng.Intn(120))
	steps := 40 + rng.Intn(40)
	discharged := false
	chooseDoc := func() string {
		if len(docIDs) == 0 {
			return "d-nope"
		}
		return docIDs[rng.Intn(len(docIDs))]
	}
	for i := 0; i < steps; i++ {
		if !discharged && now >= disAt {
			ops = append(ops, op{kind: kDischarge, now: now, enc: "e"})
			discharged = true
		}
		ts := adv()
		var o op
		switch rng.Intn(13) {
		case 0, 1:
			docSeq++
			id := fmt.Sprintf("doc%02d", docSeq)
			docIDs = append(docIDs, id)
			o = op{kind: kCreate, now: ts, user: pickUser(rng), enc: "e", doc: id, content: fmt.Sprintf("内容-%d-%d", i, rng.Intn(1000))}
		case 2:
			o = op{kind: kEdit, now: ts, user: pickUser(rng), doc: chooseDoc(), content: fmt.Sprintf("改-%d", i)}
		case 3:
			o = op{kind: kSign, now: ts, user: pickUser(rng), doc: chooseDoc()}
		case 4:
			o = op{kind: kCosign, now: ts, user: pickUser(rng), doc: chooseDoc()}
		case 5:
			o = op{kind: kReturn, now: ts, user: pickUser(rng), doc: chooseDoc()}
		case 6:
			o = op{kind: kSeal, now: ts, user: pickUser(rng), doc: chooseDoc()}
		case 7:
			o = op{kind: kAmend, now: ts, user: pickUser(rng), doc: chooseDoc(), content: fmt.Sprintf("补-%d", i)}
		case 8:
			o = op{kind: kUnseal, now: ts, a: "am", b: "ba", doc: chooseDoc()}
		case 9:
			// 非法解封：同一人
			o = op{kind: kUnseal, now: ts, a: "am", b: "am", doc: chooseDoc()}
		case 10:
			// 时钟回退
			o = op{kind: kDefects, now: ts - 1000}
		case 11:
			// 引用不存在文档
			o = op{kind: kSign, now: ts, user: "r1", doc: "ghost"}
		default:
			o = op{kind: kDefects, now: ts}
		}
		ops = append(ops, o)
	}
	return ops
}

func TestNaiveDifferential(t *testing.T) {
	if !testing.Verbose() {
		t.Log("使用 -v 运行可查看每步输入、输出与判定依据")
	}
	for seed := int64(0); seed < 1500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s, n := initWorld()
		ops := genOps(rng)
		t.Run(fmt.Sprintf("seed%04d", seed), func(t *testing.T) {
			for i, o := range ops {
				catN, whyN := n.step(o)
				catR, whyR := runReal(s, o)
				if testing.Verbose() {
					t.Logf("seed=%d step=%03d | 输入: %-60s | 朴素: %-9s(%s) | 实现: %-9s(%s)",
						seed, i, o.String(), catN, whyN, catR, whyR)
				}
				if catN != catR {
					t.Fatalf("判定不一致: 朴素=%s(%s) 实现=%s(%s)", catN, whyN, catR, whyR)
				}
				if o.kind == kDefects && catN == cOK && whyN != whyR {
					t.Fatalf("缺陷清单不一致: %q != %q", whyN, whyR)
				}
				if catN == cOK {
					cmpState(t, s, n, i, o)
				}
			}
			// 序列末尾对每份文档 Verify：两条链完好且 sealHash 与当前代数一致。
			for _, id := range s.DocIDs() {
				v, err := seal.Verify(s, id)
				if err != nil {
					t.Fatalf("verify %s: %v", id, err)
				}
				if !v.VersionChainOK || !v.AmendChainOK {
					t.Fatalf("seed %d verify %s: %+v", seed, id, v)
				}
				nd := n.docs[id]
				if nd.gen == 0 {
					if v.SealHash != nil || v.Gen != 0 {
						t.Fatalf("seed %d %s: 未封存文档 verify sealHash 非空", seed, id)
					}
					continue
				}
				var head []byte = nZero
				if len(nd.vers) > 0 {
					head = nd.vers[len(nd.vers)-1].hash
				}
				want := func() []byte {
					h := sha256.New()
					h.Write(head)
					var b [8]byte
					binary.BigEndian.PutUint64(b[:], uint64(nd.gen))
					h.Write(b[:])
					return h.Sum(nil)
				}()
				if string(v.SealHash) != string(want) || v.Gen != nd.gen {
					t.Fatalf("seed %d %s verify sealHash=%x want=%x gen=%d/%d", seed, id, v.SealHash, want, v.Gen, nd.gen)
				}
			}
		})
	}
}
