package overlay

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---------- 朴素模型：按定义逐路径解析 ----------

type mEntry struct {
	isDir   bool
	content []byte
}

type mRec struct {
	kind    string // "file" | "dir" | "opaque" | "whiteout"
	content []byte
}

// model 直接按下层映射与上层记录逐路径解析合并视图。
type model struct {
	lower map[string]mEntry
	upper map[string]mRec
}

func newModel(lower map[string][]byte) *model {
	m := &model{lower: map[string]mEntry{}, upper: map[string]mRec{}}
	for k, v := range lower {
		if strings.HasSuffix(k, "/") {
			m.lower[strings.TrimSuffix(k, "/")] = mEntry{isDir: true}
		} else {
			m.lower[k] = mEntry{content: append([]byte(nil), v...)}
		}
	}
	return m
}

func (m *model) lowerReachable(q string) bool {
	if _, ok := m.lower[q]; !ok {
		return false
	}
	for a := parent(q); a != ""; a = parent(a) {
		if r, ok := m.upper[a]; ok && r.kind == "opaque" {
			return false
		}
	}
	return true
}

// resolve 按解析定义求 p 处的条目。
func (m *model) resolve(p string) (isDir bool, content []byte, ok bool) {
	if p == "" {
		return true, nil, true
	}
	if r, has := m.upper[p]; has {
		switch r.kind {
		case "whiteout":
			return false, nil, false
		case "file":
			return false, r.content, true
		default:
			return true, nil, true
		}
	}
	if m.lowerReachable(p) {
		e := m.lower[p]
		return e.isDir, e.content, true
	}
	return false, nil, false
}

func (m *model) readDir(p string) []string {
	set := map[string]struct{}{}
	prefix := ""
	if p != "" {
		prefix = p + "/"
	}
	for q, r := range m.upper {
		if parent(q) == p && r.kind != "whiteout" {
			set[q[len(prefix):]] = struct{}{}
		}
	}
	opaque := false
	if r, ok := m.upper[p]; ok && r.kind == "opaque" {
		opaque = true
	}
	if !opaque && (p == "" || m.lowerReachable(p)) {
		if p == "" || m.lower[p].isDir {
			for q := range m.lower {
				if parent(q) == p {
					if _, covered := m.upper[q]; !covered {
						set[q[len(prefix):]] = struct{}{}
					}
				}
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (m *model) checkAnc(p string) error {
	var as []string
	for a := parent(p); a != ""; a = parent(a) {
		as = append(as, a)
	}
	// 自根向下。
	for i := len(as) - 1; i >= 0; i-- {
		isDir, _, ok := m.resolve(as[i])
		if !ok {
			return ErrNotFound
		}
		if !isDir {
			return ErrNotDir
		}
	}
	return nil
}

func (m *model) ensureAnc(p string) {
	var as []string
	for a := parent(p); a != ""; a = parent(a) {
		as = append(as, a)
	}
	for i := len(as) - 1; i >= 0; i-- {
		if _, ok := m.upper[as[i]]; !ok {
			m.upper[as[i]] = mRec{kind: "dir"}
		}
	}
}

func (m *model) drop(p string) {
	for q := range m.upper {
		if q == p || strings.HasPrefix(q, p+"/") {
			delete(m.upper, q)
		}
	}
}

func (m *model) Lookup(p string) (Entry, error) {
	if !validOpPath(p, true) {
		return Entry{}, ErrInvalidPath
	}
	if err := m.checkAnc(p); err != nil {
		return Entry{}, err
	}
	isDir, content, ok := m.resolve(p)
	if !ok {
		return Entry{}, ErrNotFound
	}
	if isDir {
		return Entry{Type: EntryDir}, nil
	}
	return Entry{Type: EntryFile, Content: append([]byte(nil), content...)}, nil
}

func (m *model) ReadDir(p string) ([]string, error) {
	if !validOpPath(p, true) {
		return nil, ErrInvalidPath
	}
	if err := m.checkAnc(p); err != nil {
		return nil, err
	}
	isDir, _, ok := m.resolve(p)
	if !ok {
		return nil, ErrNotFound
	}
	if !isDir {
		return nil, ErrNotDir
	}
	return m.readDir(p), nil
}

func (m *model) Mkdir(p string) error {
	if !validOpPath(p, false) {
		return ErrInvalidPath
	}
	if err := m.checkAnc(p); err != nil {
		return err
	}
	if _, _, ok := m.resolve(p); ok {
		return ErrExist
	}
	m.ensureAnc(p)
	if r, ok := m.upper[p]; ok && r.kind == "whiteout" {
		m.upper[p] = mRec{kind: "opaque"}
	} else {
		m.upper[p] = mRec{kind: "dir"}
	}
	return nil
}

func (m *model) Write(p string, content []byte) error {
	if !validOpPath(p, false) {
		return ErrInvalidPath
	}
	if err := m.checkAnc(p); err != nil {
		return err
	}
	if isDir, _, ok := m.resolve(p); ok && isDir {
		return ErrIsDir
	}
	m.ensureAnc(p)
	m.upper[p] = mRec{kind: "file", content: append([]byte(nil), content...)}
	return nil
}

func (m *model) Remove(p string) error {
	if !validOpPath(p, false) {
		return ErrInvalidPath
	}
	if err := m.checkAnc(p); err != nil {
		return err
	}
	isDir, _, ok := m.resolve(p)
	if !ok {
		return ErrNotFound
	}
	if isDir && len(m.readDir(p)) > 0 {
		return ErrNotEmpty
	}
	if m.lowerReachable(p) {
		m.ensureAnc(p)
		m.drop(p)
		m.upper[p] = mRec{kind: "whiteout"}
	} else {
		m.drop(p)
	}
	return nil
}

func (m *model) Rename(oldP, newP string) error {
	if !validOpPath(oldP, false) {
		return ErrInvalidPath
	}
	if err := m.checkAnc(oldP); err != nil {
		return err
	}
	oldIsDir, oldContent, ok := m.resolve(oldP)
	if !ok {
		return ErrNotFound
	}
	if !validOpPath(newP, false) {
		return ErrInvalidPath
	}
	if err := m.checkAnc(newP); err != nil {
		return err
	}
	if newP == oldP || strings.HasPrefix(newP, oldP+"/") {
		return ErrRenameSelf
	}
	oldUpper, hasOldUpper := m.upper[oldP]
	oldReach := m.lowerReachable(oldP)
	if !hasOldUpper && oldReach && m.lower[oldP].isDir {
		return ErrCrossLayer
	}
	if hasOldUpper && oldUpper.kind == "dir" && oldReach && m.lower[oldP].isDir {
		return ErrCrossLayer
	}
	newIsDir, _, newExists := m.resolve(newP)
	if newExists {
		switch {
		case !oldIsDir && newIsDir:
			return ErrIsDir
		case oldIsDir && !newIsDir:
			return ErrNotDir
		case oldIsDir && newIsDir:
			if len(m.readDir(newP)) > 0 {
				return ErrNotEmpty
			}
		}
	}
	if !oldIsDir {
		// 文件：先 Write(new) 再 Remove(old)。
		if err := m.Write(newP, oldContent); err != nil {
			return err
		}
		return m.Remove(oldP)
	}
	// 纯上层目录。
	_, newIsWhiteout := m.upper[newP]
	if newIsWhiteout {
		newIsWhiteout = m.upper[newP].kind == "whiteout"
	}
	newLowerDir := false
	if e, ok := m.lower[newP]; ok && e.isDir && m.lowerReachable(newP) {
		newLowerDir = true
	}
	m.drop(newP)
	m.ensureAnc(newP)
	var moved []string
	for q := range m.upper {
		if q == oldP || strings.HasPrefix(q, oldP+"/") {
			moved = append(moved, q)
		}
	}
	sort.Strings(moved)
	for _, q := range moved {
		r := m.upper[q]
		delete(m.upper, q)
		m.upper[newP+q[len(oldP):]] = r
	}
	rec := m.upper[newP]
	if oldUpper.kind == "opaque" || newIsWhiteout || newLowerDir {
		rec.kind = "opaque"
	}
	m.upper[newP] = rec
	if oldReach {
		m.ensureAnc(oldP)
		m.upper[oldP] = mRec{kind: "whiteout"}
	}
	return nil
}

func (m *model) upperStrings() []string {
	out := []string{}
	for p, r := range m.upper {
		if r.kind == "file" {
			out = append(out, p+" file "+string(r.content))
		} else {
			out = append(out, p+" "+r.kind)
		}
	}
	sort.Strings(out)
	return out
}

// ---------- 随机操作序列对照 ----------

// reason 把错误归约为判定依据名称。
func reason(err error) string {
	switch {
	case err == nil:
		return "成功"
	case errors.Is(err, ErrInvalidPath):
		return "非法路径"
	case errors.Is(err, ErrNotFound):
		return "未找到"
	case errors.Is(err, ErrNotDir):
		return "不是目录"
	case errors.Is(err, ErrIsDir):
		return "是目录"
	case errors.Is(err, ErrExist):
		return "已存在"
	case errors.Is(err, ErrNotEmpty):
		return "目录非空"
	case errors.Is(err, ErrCrossLayer):
		return "跨层改名"
	case errors.Is(err, ErrRenameSelf):
		return "移入自身"
	default:
		return "未知错误"
	}
}

var randNames = []string{"a", "b", "c", "d"}

func randPath(rng *rand.Rand) string {
	if rng.Intn(24) == 0 {
		bad := []string{"", "a//b", "a/.", "a/..", ".", "..", "a/b/", "/a"}
		return bad[rng.Intn(len(bad))]
	}
	depth := 1 + rng.Intn(3)
	segs := make([]string, depth)
	for i := range segs {
		segs[i] = randNames[rng.Intn(len(randNames))]
	}
	return strings.Join(segs, "/")
}

func randLower(rng *rand.Rand) map[string][]byte {
	lower := map[string][]byte{}
	dirs := []string{""}
	for i := 0; i < rng.Intn(5); i++ {
		par := dirs[rng.Intn(len(dirs))]
		name := randNames[rng.Intn(len(randNames))]
		p := name
		if par != "" {
			p = par + "/" + name
		}
		if _, dup := lower[p+"/"]; dup {
			continue
		}
		if _, dup := lower[p]; dup {
			continue
		}
		lower[p+"/"] = nil
		dirs = append(dirs, p)
	}
	for i := 0; i < rng.Intn(6); i++ {
		par := dirs[rng.Intn(len(dirs))]
		name := randNames[rng.Intn(len(randNames))]
		p := name
		if par != "" {
			p = par + "/" + name
		}
		if _, dup := lower[p+"/"]; dup {
			continue
		}
		if _, dup := lower[p]; dup {
			continue
		}
		contents := []string{"", "x", "y", "1", "22"}
		lower[p] = []byte(contents[rng.Intn(len(contents))])
	}
	return lower
}

type randOp struct {
	kind    string
	a, b    string
	content []byte
}

func randOps(rng *rand.Rand, n int) []randOp {
	ops := make([]randOp, 0, n)
	for i := 0; i < n; i++ {
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24:
			ops = append(ops, randOp{kind: "write", a: randPath(rng),
				content: []byte(fmt.Sprintf("c%d", rng.Intn(100)))})
		case 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39:
			ops = append(ops, randOp{kind: "mkdir", a: randPath(rng)})
		case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54:
			ops = append(ops, randOp{kind: "remove", a: randPath(rng)})
		case 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74:
			ops = append(ops, randOp{kind: "rename", a: randPath(rng), b: randPath(rng)})
		case 75, 76, 77, 78, 79, 80, 81, 82, 83, 84:
			ops = append(ops, randOp{kind: "lookup", a: randPath(rng)})
		default:
			ops = append(ops, randOp{kind: "readdir", a: randPath(rng)})
		}
	}
	return ops
}

// checkUpperInvariants 校验：上层任何记录的真祖先在上层都是目录
// 记录，且不存在位于白障或文件之下的记录。
func checkUpperInvariants(t *testing.T, fs *FS) {
	t.Helper()
	recs := fs.Upper()
	byPath := map[string]Record{}
	for _, r := range recs {
		byPath[r.Path] = r
	}
	for i := 1; i < len(recs); i++ {
		if recs[i-1].Path >= recs[i].Path {
			t.Fatalf("Upper() 未按路径升序: %q >= %q", recs[i-1].Path, recs[i].Path)
		}
	}
	for _, r := range recs {
		for a := parent(r.Path); a != ""; a = parent(a) {
			ar, ok := byPath[a]
			if !ok {
				t.Fatalf("记录 %q 的真祖先 %q 在上层无记录", r.Path, a)
			}
			if ar.Kind != "dir" && ar.Kind != "opaque" {
				t.Fatalf("记录 %q 位于 %s 之下", r.Path, ar.Kind)
			}
		}
	}
}

// checkViewConsistency 校验 ReadDir 列出的每个名字都能 Lookup 到，
// 未列出的名字 Lookup 不到。
func checkViewConsistency(t *testing.T, fs *FS) {
	t.Helper()
	candidates := []string{"a", "b", "c", "d", "e", "x", "zz"}
	queue := []string{""}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		names, err := fs.ReadDir(p)
		if err != nil {
			t.Fatalf("ReadDir(%q): %v", p, err)
		}
		for i := 1; i < len(names); i++ {
			if names[i-1] >= names[i] {
				t.Fatalf("ReadDir(%q) 未按字节序升序: %q", p, names)
			}
		}
		listed := map[string]bool{}
		for _, n := range names {
			listed[n] = true
			c := n
			if p != "" {
				c = p + "/" + n
			}
			e, err := fs.Lookup(c)
			if err != nil {
				t.Fatalf("ReadDir(%q) 列出 %q 但 Lookup 失败: %v", p, c, err)
			}
			if e.Type == EntryDir {
				queue = append(queue, c)
			}
		}
		for _, n := range candidates {
			if listed[n] {
				continue
			}
			c := n
			if p != "" {
				c = p + "/" + n
			}
			if _, err := fs.Lookup(c); !errors.Is(err, ErrNotFound) {
				t.Fatalf("ReadDir(%q) 未列出 %q 但 Lookup err=%v", p, c, err)
			}
		}
	}
}

func applyOp(fs *FS, m *model, op randOp) (fsOut, mOut string, fsErr, mErr error) {
	switch op.kind {
	case "write":
		fsErr = fs.Write(op.a, op.content)
		mErr = m.Write(op.a, op.content)
	case "mkdir":
		fsErr = fs.Mkdir(op.a)
		mErr = m.Mkdir(op.a)
	case "remove":
		fsErr = fs.Remove(op.a)
		mErr = m.Remove(op.a)
	case "rename":
		fsErr = fs.Rename(op.a, op.b)
		mErr = m.Rename(op.a, op.b)
	case "lookup":
		fe, err := fs.Lookup(op.a)
		me, merr := m.Lookup(op.a)
		fsErr, mErr = err, merr
		if err == nil && merr == nil {
			if fe.Type != me.Type || !reflect.DeepEqual(fe.Content, me.Content) {
				fsOut = fmt.Sprintf("type=%d content=%q", fe.Type, fe.Content)
				mOut = fmt.Sprintf("type=%d content=%q", me.Type, me.Content)
			}
		}
	case "readdir":
		fn, err := fs.ReadDir(op.a)
		mn, merr := m.ReadDir(op.a)
		fsErr, mErr = err, merr
		if err == nil && merr == nil && !reflect.DeepEqual(fn, mn) {
			fsOut = fmt.Sprintf("%q", fn)
			mOut = fmt.Sprintf("%q", mn)
		}
	}
	return fsOut, mOut, fsErr, mErr
}

// TestRandomAgainstModel 用 2000 组随机操作序列对照朴素模型，
// 日志打印输入、输出与判定依据。
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 2000
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		lower := randLower(rng)
		fs, err := New(lower)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		m := newModel(lower)
		ops := randOps(rng, 30)
		lowerKeys := make([]string, 0, len(lower))
		for k := range lower {
			lowerKeys = append(lowerKeys, k)
		}
		sort.Strings(lowerKeys)
		t.Logf("seed=%d 输入 lower=%q 操作数=%d", seed, lowerKeys, len(ops))
		for i, op := range ops {
			fsOut, mOut, fsErr, mErr := applyOp(fs, m, op)
			if reason(fsErr) != reason(mErr) || fsOut != mOut {
				t.Fatalf("seed=%d op=%d %s a=%q b=%q: FS(%s,%s) != 模型(%s,%s)",
					seed, i, op.kind, op.a, op.b, fsOut, reason(fsErr), mOut, reason(mErr))
			}
			t.Logf("seed=%d op=%d 输入=%s a=%q b=%q content=%q 输出=%s 判定依据=%s",
				seed, i, op.kind, op.a, op.b, op.content, fsOut, reason(fsErr))
			if op.kind == "lookup" || op.kind == "readdir" {
				continue
			}
			if fsErr == nil || mErr == nil {
				if fsErr != nil || mErr != nil {
					t.Fatalf("seed=%d op=%d: 拒绝状态不一致", seed, i)
				}
			}
			got, want := upperStrings(fs), m.upperStrings()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d op=%d %s: Upper()=%q, 模型=%q",
					seed, i, op.kind, got, want)
			}
			checkUpperInvariants(t, fs)
		}
		checkViewConsistency(t, fs)
		checkUpperInvariants(t, fs)
		// 重放确定性：相同操作序列得到完全相同的上层记录。
		fs2, err := New(lower)
		if err != nil {
			t.Fatalf("seed=%d replay New: %v", seed, err)
		}
		m2 := newModel(lower)
		for _, op := range ops {
			applyOp(fs2, m2, op)
		}
		if got, want := upperStrings(fs2), upperStrings(fs); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d 重放不一致: %q != %q", seed, got, want)
		}
	}
}

// TestConcurrent 并发调用所有方法，验证无数据竞争且结果等价于
// 某个串行顺序（由互斥锁保证），结束后视图自洽。
func TestConcurrent(t *testing.T) {
	lower := map[string][]byte{
		"a/": nil, "a/f": []byte("af"), "b/": nil, "b/g": []byte("bg"),
		"c": []byte("c"),
	}
	fs, err := New(lower)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const rounds = 200
	done := make(chan struct{}, workers)
	for w := 0; w < workers; w++ {
		go func(seed int64) {
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < rounds; i++ {
				ops := randOps(rng, 1)
				op := ops[0]
				switch op.kind {
				case "write":
					_ = fs.Write(op.a, op.content)
				case "mkdir":
					_ = fs.Mkdir(op.a)
				case "remove":
					_ = fs.Remove(op.a)
				case "rename":
					_ = fs.Rename(op.a, op.b)
				case "lookup":
					_, _ = fs.Lookup(op.a)
				default:
					_, _ = fs.ReadDir(op.a)
				}
				_ = fs.Upper()
			}
			done <- struct{}{}
		}(int64(w*1000 + 7))
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	checkUpperInvariants(t, fs)
	checkViewConsistency(t, fs)
}
