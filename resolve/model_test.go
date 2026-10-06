package resolve

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/alias"
	"ontology/indexreg"
)

// classify 把实现侧错误归约为可比较的规范串（含下标与名字细节）。
func classify(err error) string {
	if err == nil {
		return "ok"
	}
	var se *alias.StepError
	if errors.As(err, &se) {
		return fmt.Sprintf("step:%d:%s", se.Index, classify(se.Err))
	}
	var ce *indexreg.ConflictError
	if errors.As(err, &ce) {
		return "conflict:" + ce.Name
	}
	var mw *alias.MultiWriteError
	if errors.As(err, &mw) {
		return "multiwrite:" + mw.Alias
	}
	switch {
	case errors.Is(err, indexreg.ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, indexreg.ErrIndexNotFound):
		return "idxnotfound"
	case errors.Is(err, indexreg.ErrIndexClosed):
		return "closed"
	case errors.Is(err, indexreg.ErrNameNotFound):
		return "namenotfound"
	case errors.Is(err, alias.ErrMemberNotFound):
		return "membernotfound"
	case errors.Is(err, ErrNoWriteIndex):
		return "nowrite"
	}
	return "unknown:" + err.Error()
}

func canonRead(targets []ReadTarget, err error) string {
	if err != nil {
		return classify(err)
	}
	parts := make([]string, 0, len(targets))
	for _, tg := range targets {
		f := "-"
		if tg.Filter != nil {
			f = *tg.Filter
		}
		parts = append(parts, tg.Index+":"+f)
	}
	return "read:" + strings.Join(parts, ",")
}

func canonWrite(got string, err error) string {
	if err != nil {
		return classify(err)
	}
	return "write:" + got
}

// model 是按规格逐条写成的逐步朴素模拟，作为对照基准。
type model struct {
	indices map[string]bool // 名字 -> 是否关闭
	aliases map[string]map[string]indexreg.Member
	epoch   int
}

func newModel() *model {
	return &model{
		indices: make(map[string]bool),
		aliases: make(map[string]map[string]indexreg.Member),
	}
}

func (m *model) createIndex(name string) (out, why string) {
	if !indexreg.ValidName(name) {
		return "invalid", "依据=参数非法:名字非法"
	}
	if _, ok := m.indices[name]; ok {
		return "conflict:" + name, "依据=与现有索引同名"
	}
	if _, ok := m.aliases[name]; ok {
		return "conflict:" + name, "依据=与现有别名同名"
	}
	m.indices[name] = false
	m.epoch++
	return "ok", "依据=接受:新建索引,纪元+1"
}

func (m *model) setClosed(name string, closed bool) (out, why string) {
	c, ok := m.indices[name]
	if !ok {
		return "idxnotfound", "依据=索引不存在"
	}
	if c == closed {
		return "ok", "依据=空操作:状态本已如此,纪元不变"
	}
	m.indices[name] = closed
	m.epoch++
	return "ok", "依据=接受:开闭状态翻转,纪元+1"
}

func (m *model) update(actions []alias.Action) (out, why string) {
	if len(actions) < 1 || len(actions) > 100 {
		return "invalid", "依据=参数非法:动作数越界"
	}
	for _, a := range actions {
		if !validAction(a) {
			return "invalid", "依据=参数非法:名字或种类非法"
		}
	}
	w := m.clone()
	for i, a := range actions {
		if sOut, sWhy, bad := w.step(a, i); bad {
			return sOut, sWhy
		}
	}
	if name, ok := w.conflict(); ok {
		return "conflict:" + name, "依据=终态:名字冲突(" + name + ")"
	}
	if name, ok := w.multiWrite(); ok {
		return "multiwrite:" + name, "依据=终态:多写索引(" + name + ")"
	}
	if w.equal(m) {
		return "ok", "依据=接受:终态与提交前相同,纪元不变"
	}
	m.indices, m.aliases = w.indices, w.aliases
	m.epoch++
	return "ok", "依据=接受:状态改变,纪元+1"
}

func validAction(a alias.Action) bool {
	switch a.Kind {
	case alias.KindAdd, alias.KindRemove:
		return indexreg.ValidName(a.Alias) && indexreg.ValidName(a.Index)
	case alias.KindRemoveIndex:
		return indexreg.ValidName(a.Index)
	}
	return false
}

// step 在工作副本上应用一个动作；bad 为真表示该步报错（带下标）。
func (m *model) step(a alias.Action, i int) (out, why string, bad bool) {
	switch a.Kind {
	case alias.KindAdd:
		if _, ok := m.indices[a.Index]; !ok {
			return fmt.Sprintf("step:%d:idxnotfound", i),
				fmt.Sprintf("依据=逐步:动作%d的Add索引%q不是索引", i, a.Index), true
		}
		al := m.aliases[a.Alias]
		if al == nil {
			al = make(map[string]indexreg.Member)
			m.aliases[a.Alias] = al
		}
		al[a.Index] = indexreg.Member{IsWrite: a.IsWrite, Filter: cloneStr(a.Filter)}
	case alias.KindRemove:
		al := m.aliases[a.Alias]
		if _, ok := al[a.Index]; !ok {
			if a.MustExist {
				return fmt.Sprintf("step:%d:membernotfound", i),
					fmt.Sprintf("依据=逐步:动作%d的成员(%q,%q)不存在且mustExist", i, a.Alias, a.Index), true
			}
			return "", "", false
		}
		delete(al, a.Index)
		if len(al) == 0 {
			delete(m.aliases, a.Alias)
		}
	case alias.KindRemoveIndex:
		if _, ok := m.indices[a.Index]; !ok {
			return fmt.Sprintf("step:%d:idxnotfound", i),
				fmt.Sprintf("依据=逐步:动作%d的RemoveIndex目标%q不是索引", i, a.Index), true
		}
		delete(m.indices, a.Index)
		for name, al := range m.aliases {
			delete(al, a.Index)
			if len(al) == 0 {
				delete(m.aliases, name)
			}
		}
	}
	return "", "", false
}

func (m *model) conflict() (string, bool) {
	best := ""
	for name := range m.aliases {
		if _, ok := m.indices[name]; ok && (best == "" || name < best) {
			best = name
		}
	}
	return best, best != ""
}

func (m *model) multiWrite() (string, bool) {
	best := ""
	for name, al := range m.aliases {
		n := 0
		for _, mem := range al {
			if mem.IsWrite == indexreg.WriteTrue {
				n++
			}
		}
		if n > 1 && (best == "" || name < best) {
			best = name
		}
	}
	return best, best != ""
}

func deriveWrite(al map[string]indexreg.Member) (bool, string) {
	nTrue := 0
	write := ""
	for idx, mem := range al {
		if mem.IsWrite == indexreg.WriteTrue {
			nTrue++
			write = idx
		}
	}
	if nTrue == 1 {
		return true, write
	}
	if nTrue == 0 && len(al) == 1 {
		for idx, mem := range al {
			if mem.IsWrite == indexreg.WriteUnspecified {
				return true, idx
			}
		}
	}
	return false, ""
}

func (m *model) read(name string) string {
	if closed, ok := m.indices[name]; ok {
		if closed {
			return "closed"
		}
		return "read:" + name + ":-"
	}
	al, ok := m.aliases[name]
	if !ok {
		return "namenotfound"
	}
	names := make([]string, 0, len(al))
	for idx := range al {
		names = append(names, idx)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, idx := range names {
		if m.indices[idx] {
			continue
		}
		f := "-"
		if mem := al[idx]; mem.Filter != nil {
			f = *mem.Filter
		}
		parts = append(parts, idx+":"+f)
	}
	return "read:" + strings.Join(parts, ",")
}

func (m *model) write(name string) string {
	if closed, ok := m.indices[name]; ok {
		if closed {
			return "closed"
		}
		return "write:" + name
	}
	al, ok := m.aliases[name]
	if !ok {
		return "namenotfound"
	}
	has, w := deriveWrite(al)
	if !has {
		return "nowrite"
	}
	if m.indices[w] {
		return "closed"
	}
	return "write:" + w
}

func (m *model) clone() *model {
	w := &model{
		indices: make(map[string]bool, len(m.indices)),
		aliases: make(map[string]map[string]indexreg.Member, len(m.aliases)),
		epoch:   m.epoch,
	}
	for name, closed := range m.indices {
		w.indices[name] = closed
	}
	for name, al := range m.aliases {
		cp := make(map[string]indexreg.Member, len(al))
		for idx, mem := range al {
			cp[idx] = indexreg.Member{IsWrite: mem.IsWrite, Filter: cloneStr(mem.Filter)}
		}
		w.aliases[name] = cp
	}
	return w
}

func (m *model) equal(o *model) bool {
	if len(m.indices) != len(o.indices) || len(m.aliases) != len(o.aliases) {
		return false
	}
	for name, closed := range m.indices {
		oc, ok := o.indices[name]
		if !ok || closed != oc {
			return false
		}
	}
	for name, al := range m.aliases {
		oal, ok := o.aliases[name]
		if !ok || len(al) != len(oal) {
			return false
		}
		for idx, mem := range al {
			om, ok := oal[idx]
			if !ok || mem.IsWrite != om.IsWrite || !strEq(mem.Filter, om.Filter) {
				return false
			}
		}
	}
	return true
}

func cloneStr(s *string) *string {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

func strEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func pickName(rnd *rand.Rand, valid, invalid []string) string {
	if rnd.Intn(10) == 0 {
		return invalid[rnd.Intn(len(invalid))]
	}
	return valid[rnd.Intn(len(valid))]
}

func genAction(rnd *rand.Rand, valid, invalid []string) alias.Action {
	switch rnd.Intn(10) {
	case 0, 1, 2:
		return alias.Remove(pickName(rnd, valid, invalid), pickName(rnd, valid, invalid), rnd.Intn(2) == 0)
	case 3, 4:
		return alias.RemoveIndex(pickName(rnd, valid, invalid))
	default:
		var f *string
		if rnd.Intn(2) == 0 {
			s := fmt.Sprintf("f%d", rnd.Intn(4))
			f = &s
		}
		return alias.Add(pickName(rnd, valid, invalid), pickName(rnd, valid, invalid),
			indexreg.WriteFlag(rnd.Intn(3)), f)
	}
}

func describeActions(acts []alias.Action) string {
	parts := make([]string, len(acts))
	for i, a := range acts {
		switch a.Kind {
		case alias.KindAdd:
			f := "nil"
			if a.Filter != nil {
				f = *a.Filter
			}
			parts[i] = fmt.Sprintf("Add(%s,%s,%d,%s)", a.Alias, a.Index, a.IsWrite, f)
		case alias.KindRemove:
			parts[i] = fmt.Sprintf("Remove(%s,%s,%t)", a.Alias, a.Index, a.MustExist)
		case alias.KindRemoveIndex:
			parts[i] = fmt.Sprintf("RemoveIndex(%s)", a.Index)
		}
	}
	return strings.Join(parts, ";")
}

// TestRandomModel 用 1500 组随机操作序列把实现与逐步朴素模拟逐操作对照，
// 日志打印输入、输出与判定依据；每次操作后还对照全部名字的读写解析与纪元。
func TestRandomModel(t *testing.T) {
	const sequences = 1500
	rnd := rand.New(rand.NewSource(1370))
	valid := []string{"i1", "i2", "i3", "i4", "a", "b", "c", "x"}
	invalid := []string{"", "A", "-q", strings.Repeat("z", 65)}
	probes := append(append([]string{}, valid...), "ghost", "A")
	t.Logf("随机种子=1370 序列数=%d 判定依据=与逐步朴素模拟逐操作对照错误类别/细节、读写解析与纪元", sequences)
	for seq := 0; seq < sequences; seq++ {
		r := indexreg.New()
		m := newModel()
		ops := 8 + rnd.Intn(20)
		for k := 0; k < ops; k++ {
			var in, realOut, modelOut, why string
			switch rnd.Intn(10) {
			case 0, 1:
				name := pickName(rnd, valid, invalid)
				in = fmt.Sprintf("CreateIndex(%q)", name)
				realOut = classify(r.CreateIndex(name))
				modelOut, why = m.createIndex(name)
			case 2, 3:
				name := pickName(rnd, valid, invalid)
				in = fmt.Sprintf("CloseIndex(%q)", name)
				realOut = classify(r.CloseIndex(name))
				modelOut, why = m.setClosed(name, true)
			case 4, 5:
				name := pickName(rnd, valid, invalid)
				in = fmt.Sprintf("OpenIndex(%q)", name)
				realOut = classify(r.OpenIndex(name))
				modelOut, why = m.setClosed(name, false)
			default:
				var acts []alias.Action
				if rnd.Intn(8) == 0 {
					// 定向批次：同一别名放两个显式真成员，锻炼终态多写索引检查。
					al := valid[rnd.Intn(len(valid))]
					acts = []alias.Action{
						alias.Add(al, valid[rnd.Intn(len(valid))], indexreg.WriteTrue, nil),
						alias.Add(al, valid[rnd.Intn(len(valid))], indexreg.WriteTrue, nil),
					}
					for len(acts) < 5 && rnd.Intn(2) == 0 {
						acts = append(acts, genAction(rnd, valid, invalid))
					}
				} else {
					n := 1 + rnd.Intn(4)
					switch rnd.Intn(30) {
					case 0:
						n = 0
					case 1:
						n = 101
					}
					acts = make([]alias.Action, n)
					for i := range acts {
						acts[i] = genAction(rnd, valid, invalid)
					}
				}
				in = "Update[" + describeActions(acts) + "]"
				realOut = classify(alias.Update(r, acts))
				modelOut, why = m.update(acts)
			}
			if realOut != modelOut {
				t.Fatalf("seq=%d op=%d 输入=%s: 实现=%s 模型=%s %s", seq, k, in, realOut, modelOut, why)
			}
			if got, want := r.Epoch(), uint64(m.epoch); got != want {
				t.Fatalf("seq=%d op=%d 输入=%s: 纪元 实现=%d 模型=%d %s", seq, k, in, got, want, why)
			}
			t.Logf("seq=%d op=%d 输入=%s 输出=%s 纪元=%d %s", seq, k, in, realOut, m.epoch, why)
			for _, name := range probes {
				rt, rerr := ResolveRead(r, name)
				if got, want := canonRead(rt, rerr), m.read(name); got != want {
					t.Fatalf("seq=%d op=%d ResolveRead(%q): 实现=%s 模型=%s", seq, k, name, got, want)
				}
				wr, werr := ResolveWrite(r, name)
				if got, want := canonWrite(wr, werr), m.write(name); got != want {
					t.Fatalf("seq=%d op=%d ResolveWrite(%q): 实现=%s 模型=%s", seq, k, name, got, want)
				}
			}
		}
		t.Logf("seq=%d 完成 ops=%d 最终纪元=%d", seq, ops, m.epoch)
	}
}

// TestConcurrentSmoke 并发调用所有操作；在 -race 下验证串行等价性不破裂。
func TestConcurrentSmoke(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1", "i2", "i3", "i4")
	names := []string{"i1", "i2", "i3", "i4", "a", "b"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for k := 0; k < 300; k++ {
				n1 := names[rnd.Intn(len(names))]
				n2 := names[rnd.Intn(len(names))]
				switch rnd.Intn(5) {
				case 0:
					_ = alias.Update(r, []alias.Action{
						alias.Add(n1, n2, indexreg.WriteFlag(rnd.Intn(3)), nil),
					})
				case 1:
					_ = r.CloseIndex(n1)
				case 2:
					_ = r.OpenIndex(n1)
				case 3:
					_, _ = ResolveRead(r, n1)
				case 4:
					_, _ = ResolveWrite(r, n1)
				}
			}
		}(int64(g))
	}
	wg.Wait()
}
