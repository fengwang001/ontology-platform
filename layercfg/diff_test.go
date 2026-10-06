package layercfg_test

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"testing"

	"ontology/layercfg"
)

// ---- 独立朴素模型：每次发布全量复制配置，不与实现共享任何代码 ----

type nEntry struct {
	cancel bool
	value  layercfg.Value
}

type nKey struct {
	writes map[layercfg.Ref]*nEntry
	locks  map[layercfg.Ref]bool
}

type nSnapshot struct {
	version int
	keys    map[string]*nKey
}

type nSchema struct {
	typ      layercfg.ValueType
	required bool
	min, max int64
	hasRange bool
	merge    layercfg.MergeMode
}

type naiveModel struct {
	schemas map[string]nSchema
	current *nSnapshot
	history []*nSnapshot
}

func newNaive() *naiveModel {
	base := &nSnapshot{version: 0, keys: map[string]*nKey{}}
	return &naiveModel{schemas: map[string]nSchema{}, current: base, history: []*nSnapshot{base}}
}

func ncloneSnap(src *nSnapshot, newVersion int) *nSnapshot {
	cp := &nSnapshot{version: newVersion, keys: map[string]*nKey{}}
	for k, ks := range src.keys {
		nk := &nKey{writes: map[layercfg.Ref]*nEntry{}, locks: map[layercfg.Ref]bool{}}
		for r, e := range ks.writes {
			ne := &nEntry{cancel: e.cancel, value: e.value}
			if e.value.List != nil {
				ne.value.List = append([]string(nil), e.value.List...)
			}
			nk.writes[r] = ne
		}
		for r := range ks.locks {
			nk.locks[r] = true
		}
		cp.keys[k] = nk
	}
	return cp
}

func refsOf(sc layercfg.Scope) []layercfg.Ref {
	rs := []layercfg.Ref{{Layer: layercfg.LayerGlobal}}
	if sc.Env != "" {
		rs = append(rs, layercfg.Ref{Layer: layercfg.LayerEnv, Env: sc.Env})
	}
	if sc.Region != "" {
		rs = append(rs, layercfg.Ref{Layer: layercfg.LayerRegion, Env: sc.Env, Region: sc.Region})
	}
	if sc.Instance != "" {
		rs = append(rs, layercfg.Ref{Layer: layercfg.LayerInstance, Env: sc.Env, Region: sc.Region, Instance: sc.Instance})
	}
	return rs
}

func (m *naiveModel) resolve(snap *nSnapshot, key string, sc layercfg.Scope) layercfg.Result {
	schema, ok := m.schemas[key]
	if !ok {
		return layercfg.Result{}
	}
	var present bool
	var acc layercfg.Value
	seen := map[string]bool{}
	for _, ref := range refsOf(sc) {
		ks := snap.keys[key]
		if ks == nil {
			continue
		}
		e, ok := ks.writes[ref]
		if !ok {
			continue
		}
		if e.cancel {
			present, acc, seen = false, layercfg.Value{}, map[string]bool{}
			continue
		}
		if schema.merge == layercfg.MergeAppend {
			if !present {
				acc = layercfg.Value{Type: layercfg.TypeStringList}
			}
			for _, item := range e.value.List {
				if !seen[item] {
					seen[item] = true
					acc.List = append(acc.List, item)
				}
			}
		} else {
			acc = e.value
			if acc.List != nil {
				acc.List = append([]string(nil), acc.List...)
			}
		}
		present = true
	}
	return layercfg.Result{Present: present, Value: acc}
}

func refValidN(ref layercfg.Ref) (string, bool) {
	switch ref.Layer {
	case layercfg.LayerGlobal:
		if ref.Env != "" || ref.Region != "" || ref.Instance != "" {
			return "global ref must be bare", false
		}
	case layercfg.LayerEnv:
		if ref.Env == "" || ref.Region != "" || ref.Instance != "" {
			return "bad env ref", false
		}
	case layercfg.LayerRegion:
		if ref.Env == "" || ref.Region == "" || ref.Instance != "" {
			return "bad region ref", false
		}
	case layercfg.LayerInstance:
		if ref.Env == "" || ref.Region == "" || ref.Instance == "" {
			return "bad instance ref", false
		}
	default:
		return "bad layer", false
	}
	return "", true
}

func narrowerN(anchor, w layercfg.Ref) bool {
	if w.Layer <= anchor.Layer {
		return false
	}
	if anchor.Layer == layercfg.LayerGlobal {
		return true
	}
	if w.Env != anchor.Env {
		return false
	}
	if anchor.Layer == layercfg.LayerEnv {
		return true
	}
	if w.Region != anchor.Region {
		return false
	}
	return true
}

func entityScopesN(snap *nSnapshot) []layercfg.Scope {
	envs := map[string]bool{}
	regions := map[layercfg.Scope]bool{}
	insts := map[layercfg.Scope]bool{}
	for _, ks := range snap.keys {
		for ref := range ks.writes {
			switch ref.Layer {
			case layercfg.LayerEnv:
				envs[ref.Env] = true
			case layercfg.LayerRegion:
				envs[ref.Env] = true
				regions[layercfg.Scope{Env: ref.Env, Region: ref.Region}] = true
			case layercfg.LayerInstance:
				envs[ref.Env] = true
				regions[layercfg.Scope{Env: ref.Env, Region: ref.Region}] = true
				insts[layercfg.Scope{Env: ref.Env, Region: ref.Region, Instance: ref.Instance}] = true
			}
		}
	}
	out := []layercfg.Scope{{}}
	for e := range envs {
		out = append(out, layercfg.Scope{Env: e})
	}
	for r := range regions {
		out = append(out, r)
	}
	for i := range insts {
		out = append(out, i)
	}
	return out
}

// publishNaive 返回（新版本号，错误类别，是否成功），错误优先级与实现一致。
func (m *naiveModel) publish(changes []layercfg.Change) (int, layercfg.ErrorKind, bool) {
	// 阶段1 参数非法
	for _, ch := range changes {
		if ch.Key == "" {
			return m.current.version, layercfg.ErrInvalidArgument, false
		}
		if msg, ok := refValidN(ch.Ref); !ok {
			return m.current.version, layercfg.ErrInvalidArgument, false
		} else {
			_ = msg
		}
		switch ch.Op {
		case layercfg.OpSetValue:
			if ch.Value.Type == 0 {
				return m.current.version, layercfg.ErrInvalidArgument, false
			}
		case layercfg.OpCancel, layercfg.OpClearWrite, layercfg.OpLock, layercfg.OpUnlock:
			if ch.Value.Type != 0 {
				return m.current.version, layercfg.ErrInvalidArgument, false
			}
		default:
			return m.current.version, layercfg.ErrInvalidArgument, false
		}
	}
	// 阶段2 键未登记
	for _, ch := range changes {
		if _, ok := m.schemas[ch.Key]; !ok {
			return m.current.version, layercfg.ErrKeyNotRegistered, false
		}
	}
	// 阶段3 类型/范围
	for _, ch := range changes {
		if ch.Op == layercfg.OpSetValue {
			sc := m.schemas[ch.Key]
			if ch.Value.Type != sc.typ {
				return m.current.version, layercfg.ErrTypeOrRange, false
			}
			if sc.typ == layercfg.TypeInt && sc.hasRange && (ch.Value.Int < sc.min || ch.Value.Int > sc.max) {
				return m.current.version, layercfg.ErrTypeOrRange, false
			}
		}
	}
	// 同发布冲突
	type slot struct {
		k string
		r layercfg.Ref
	}
	ws, ls := map[slot]bool{}, map[slot]bool{}
	for _, ch := range changes {
		s := slot{ch.Key, ch.Ref}
		switch ch.Op {
		case layercfg.OpSetValue, layercfg.OpCancel, layercfg.OpClearWrite:
			if ws[s] {
				return m.current.version, layercfg.ErrConflict, false
			}
			ws[s] = true
		case layercfg.OpLock, layercfg.OpUnlock:
			if ls[s] {
				return m.current.version, layercfg.ErrConflict, false
			}
			ls[s] = true
		}
	}
	// 应用到全量复制候选
	cand := ncloneSnap(m.current, m.current.version+1)
	for _, ch := range changes {
		ks := cand.keys[ch.Key]
		if ks == nil {
			ks = &nKey{writes: map[layercfg.Ref]*nEntry{}, locks: map[layercfg.Ref]bool{}}
			cand.keys[ch.Key] = ks
		}
		switch ch.Op {
		case layercfg.OpSetValue:
			v := ch.Value
			v.List = append([]string(nil), ch.Value.List...)
			ks.writes[ch.Ref] = &nEntry{value: v}
		case layercfg.OpCancel:
			ks.writes[ch.Ref] = &nEntry{cancel: true}
		case layercfg.OpClearWrite:
			delete(ks.writes, ch.Ref)
		case layercfg.OpLock:
			ks.locks[ch.Ref] = true
		case layercfg.OpUnlock:
			delete(ks.locks, ch.Ref)
		}
		if len(ks.writes) == 0 && len(ks.locks) == 0 {
			delete(cand.keys, ch.Key)
		}
	}
	// 锁定冲突
	for key, ks := range cand.keys {
		for lr := range ks.locks {
			for wr := range ks.writes {
				if narrowerN(lr, wr) {
					return m.current.version, layercfg.ErrLockConflict, false
				}
				_ = key
			}
		}
	}
	// 必填
	for _, sc := range entityScopesN(cand) {
		for key, schema := range m.schemas {
			if schema.required && !m.resolve(cand, key, sc).Present {
				return m.current.version, layercfg.ErrRequiredMissing, false
			}
		}
	}
	m.current = cand
	m.history = append(m.history, cand)
	return cand.version, 0, true
}

func (m *naiveModel) rollback(v int) (int, layercfg.ErrorKind, bool) {
	if v < 0 {
		return m.current.version, layercfg.ErrInvalidArgument, false
	}
	if v >= len(m.history) {
		return m.current.version, layercfg.ErrVersionNotFound, false
	}
	if v == m.current.version {
		return v, 0, true
	}
	target := m.history[v]
	for _, sc := range entityScopesN(target) {
		for key, schema := range m.schemas {
			if schema.required && !m.resolve(target, key, sc).Present {
				return m.current.version, layercfg.ErrRequiredMissing, false
			}
		}
	}
	// 朴素模型语义：回滚生成一个内容完全相同但相互独立的完整副本。
	cand := ncloneSnap(target, m.current.version+1)
	m.current = cand
	m.history = append(m.history, cand)
	return cand.version, 0, true
}

type opLogger struct {
	t    *testing.T
	logs []string
}

func (l *opLogger) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.logs = append(l.logs, line)
	if len(l.logs) > 600 {
		l.logs = l.logs[len(l.logs)-600:]
	}
	l.t.Log(line)
}

func (l *opLogger) failf(format string, args ...any) {
	for _, line := range l.logs {
		l.t.Log(line)
	}
	l.t.Fatalf(format, args...)
}

func kindOf(err error) layercfg.ErrorKind {
	var e *layercfg.Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

func resultsEqual(a, b map[string]layercfg.Result) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		ar, br := a[k], b[k]
		if ar.Present != br.Present {
			return false
		}
		if ar.Present {
			if ar.Value.Type != br.Value.Type {
				return false
			}
			if !reflect.DeepEqual(ar.Value, br.Value) {
				return false
			}
		}
	}
	return true
}

func scopeValidN(sc layercfg.Scope) bool {
	if sc.Env == "" && (sc.Region != "" || sc.Instance != "") {
		return false
	}
	if sc.Region == "" && sc.Instance != "" {
		return false
	}
	return true
}

// TestRandomDifferential 大量随机发布/回滚/历史解析，与朴素模型逐步对照。
func TestRandomDifferential(t *testing.T) {
	if os.Getenv("DIFF_SEED") == "" {
		// 默认跑固定种子保证可复现；CI 可用环境变量覆盖。
		os.Setenv("DIFF_SEED", "20261006")
	}
	var seed int64
	fmt.Sscan(os.Getenv("DIFF_SEED"), &seed)
	const iterations = 4000
	for _, sd := range []int64{seed, seed + 1, seed + 2} {
		sd := sd
		t.Run(fmt.Sprintf("seed=%d", sd), func(t *testing.T) {
			rng := rand.New(rand.NewSource(sd))
			lg := &opLogger{t: t}
			store := layercfg.NewStore()
			naive := newNaive()

			envs := []string{"prod", "dev", "stg"}
			regions := []string{"cn", "us", "eu"}
			insts := []string{"i1", "i2", "i3"}
			keyNames := []string{"k0", "k1", "k2", "k3", "k4", "k5"}

			// 预登记一批混合模式的键（含必填、追加、整数范围）。
			for idx, name := range keyNames {
				spec := layercfg.SchemaSpec{Key: name}
				switch idx % 5 {
				case 0:
					spec.Type = layercfg.TypeString
				case 1:
					spec.Type = layercfg.TypeInt
					spec.Range = &layercfg.IntRange{Min: 0, Max: 100}
				case 2:
					spec.Type = layercfg.TypeBool
				case 3:
					spec.Type = layercfg.TypeStringList
					spec.Merge = layercfg.MergeAppend
				case 4:
					spec.Type = layercfg.TypeString
					spec.Required = true
				}
				if idx%5 != 3 && idx%5 != 4 {
					spec.Merge = layercfg.MergeReplace
				}
				if idx%5 == 4 {
					spec.Merge = layercfg.MergeReplace
				}
				if err := store.RegisterKey(spec); err != nil {
					t.Fatal(err)
				}
				ns := nSchema{typ: spec.Type, required: spec.Required, merge: spec.Merge}
				if spec.Range != nil {
					ns.min, ns.max, ns.hasRange = spec.Range.Min, spec.Range.Max, true
				}
				naive.schemas[name] = ns
			}

			randomRef := func() layercfg.Ref {
				switch rng.Intn(4) {
				case 0:
					return layercfg.Ref{Layer: layercfg.LayerGlobal}
				case 1:
					return layercfg.Ref{Layer: layercfg.LayerEnv, Env: envs[rng.Intn(len(envs))]}
				case 2:
					return layercfg.Ref{Layer: layercfg.LayerRegion, Env: envs[rng.Intn(len(envs))], Region: regions[rng.Intn(len(regions))]}
				default:
					return layercfg.Ref{Layer: layercfg.LayerInstance, Env: envs[rng.Intn(len(envs))], Region: regions[rng.Intn(len(regions))], Instance: insts[rng.Intn(len(insts))]}
				}
			}
			randomScope := func() layercfg.Scope {
				env := envs[rng.Intn(len(envs))]
				switch rng.Intn(4) {
				case 0:
					return layercfg.Scope{}
				case 1:
					return layercfg.Scope{Env: env}
				case 2:
					return layercfg.Scope{Env: env, Region: regions[rng.Intn(len(regions))]}
				default:
					return layercfg.Scope{Env: env, Region: regions[rng.Intn(len(regions))], Instance: insts[rng.Intn(len(insts))]}
				}
			}
			randomValue := func(name string) layercfg.Value {
				sc := naive.schemas[name]
				switch sc.typ {
				case layercfg.TypeString:
					choices := []string{"", "a", "b", "c", "x", "y"}
					return layercfg.NewString(choices[rng.Intn(len(choices))])
				case layercfg.TypeInt:
					// 偶尔生成越界值以触发类型/范围错误分支
					return layercfg.NewInt(int64(rng.Intn(120) - 10))
				case layercfg.TypeBool:
					return layercfg.NewBool(rng.Intn(2) == 1)
				default:
					n := rng.Intn(4)
					items := make([]string, n)
					pool := []string{"a", "b", "c", "d"}
					for i := range items {
						items[i] = pool[rng.Intn(len(pool))]
					}
					return layercfg.NewStringList(items)
				}
			}

			for step := 0; step < iterations; step++ {
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4, 5: // 发布一批 1~4 条变更
					n := 1 + rng.Intn(4)
					changes := make([]layercfg.Change, n)
					for i := range changes {
						key := keyNames[rng.Intn(len(keyNames))]
						ref := randomRef()
						var ch layercfg.Change
						switch rng.Intn(5) {
						case 0:
							ch = layercfg.Change{Op: layercfg.OpSetValue, Ref: ref, Key: key, Value: randomValue(key)}
						case 1:
							ch = layercfg.Change{Op: layercfg.OpCancel, Ref: ref, Key: key}
						case 2:
							ch = layercfg.Change{Op: layercfg.OpClearWrite, Ref: ref, Key: key}
						case 3:
							ch = layercfg.Change{Op: layercfg.OpLock, Ref: ref, Key: key}
						default:
							ch = layercfg.Change{Op: layercfg.OpUnlock, Ref: ref, Key: key}
						}
						changes[i] = ch
					}
					// 偶尔注入非法 ref / 未登记键 / 坏值，验证错误优先级
					if rng.Intn(20) == 0 {
						changes[0].Ref = layercfg.Ref{Layer: layercfg.LayerEnv}
					}
					if rng.Intn(20) == 1 {
						changes[0].Key = "unregistered"
					}
					lg.logf("[step %d] PUBLISH input=%v", step, changes)
					gotV, gotErr := store.Publish(changes)
					wantV, wantKind, wantOK := naive.publish(changes)
					if (gotErr == nil) != wantOK {
						lg.failf("[step %d] publish acceptance mismatch: impl err=%v, naive ok=%v kind=%d", step, gotErr, wantOK, wantKind)
					}
					if !wantOK && kindOf(gotErr) != wantKind {
						lg.failf("[step %d] publish error kind mismatch impl=%d naive=%d err=%v input=%v", step, kindOf(gotErr), wantKind, gotErr, changes)
					}
					if wantOK && gotV != wantV {
						lg.failf("[step %d] publish version mismatch: impl=%d naive=%d", step, gotV, wantV)
					}
					lg.logf("[step %d] PUBLISH actual: version=%d err=%v | verdict ok=%v kind=%d (accepted=%v)",
						step, gotV, gotErr, gotErr == nil, wantKind, wantOK)

				case 6, 7: // 解析（当前或随机历史版本）
					sc := randomScope()
					key := keyNames[rng.Intn(len(keyNames))]
					version := -1
					if rng.Intn(2) == 0 {
						version = rng.Intn(int(naive.current.version) + 2) // 偶尔越界
					}
					lg.logf("[step %d] RESOLVE input key=%s scope=%+v version=%d", step, key, sc, version)
					r1, err1 := store.Resolve(key, sc, version)
					r2 := naive.resolve(naive.current, key, sc)
					if version >= 0 && version <= int(naive.current.version) {
						r2 = naive.resolve(naive.history[version], key, sc)
					}
					switch {
					case !scopeValidN(sc):
						if kindOf(err1) != layercfg.ErrInvalidArgument {
							lg.failf("[step %d] expected invalid-argument, got %v", step, err1)
						}
					case version > int(naive.current.version):
						if kindOf(err1) != layercfg.ErrVersionNotFound {
							lg.failf("[step %d] expected version-not-found, got %v", step, err1)
						}
					default:
						if err1 != nil {
							lg.failf("[step %d] unexpected resolve err: %v", step, err1)
						}
						if !reflect.DeepEqual(r1, r2) {
							lg.failf("[step %d] resolve mismatch key=%s scope=%+v v=%d impl=%+v naive=%+v",
								step, key, sc, version, r1, r2)
						}
					}
					lg.logf("[step %d] RESOLVE actual: %+v err=%v | naive=%+v", step, r1, err1, r2)

				case 8: // 全量解析对照
					sc := randomScope()
					version := -1
					if rng.Intn(2) == 0 {
						version = rng.Intn(int(naive.current.version) + 1)
					}
					lg.logf("[step %d] RESOLVEALL scope=%+v version=%d", step, sc, version)
					all, err := store.ResolveAll(sc, version)
					if err != nil {
						lg.failf("[step %d] resolveall err: %v", step, err)
					}
					snap := naive.current
					if version >= 0 {
						snap = naive.history[version]
					}
					want := map[string]layercfg.Result{}
					for k := range naive.schemas {
						want[k] = naive.resolve(snap, k, sc)
					}
					if !resultsEqual(all, want) {
						lg.failf("[step %d] resolveall mismatch scope=%+v v=%d impl=%+v naive=%+v", step, sc, version, all, want)
					}
					lg.logf("[step %d] RESOLVEALL actual keys=%d matches naive", step, len(all))

				case 9: // 回滚
					v := rng.Intn(int(naive.current.version) + 2)
					lg.logf("[step %d] ROLLBACK input target=%d (current=%d)", step, v, naive.current.version)
					gv, gerr := store.Rollback(v)
					wv, wk, wok := naive.rollback(v)
					if (gerr == nil) != wok || (wok && gv != wv) || (!wok && kindOf(gerr) != wk) {
						lg.failf("[step %d] rollback mismatch impl=%d,%v naive=%d,kind=%d,ok=%v", step, gv, gerr, wv, wk, wok)
					}
					lg.logf("[step %d] ROLLBACK actual: newVersion=%d err=%v | naive newVersion=%d ok=%v kind=%d", step, gv, gerr, wv, wok, wk)

				} // end switch
				// 每步校验当前版本号一致。
				if store.Current() != int(naive.current.version) {
					lg.failf("[step %d] current version drift impl=%d naive=%d", step, store.Current(), naive.current.version)
				}
			}
		})
	}
}
