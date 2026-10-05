package ingest_test

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/coerce"
	"ontology/ingest"
	"ontology/mapping"
)

// sim 是按规则逐步写成的朴素模拟：字段表是扁平 map，
// 每次操作整表复制、成功才提交，与 Engine 的树形实现互独立。
type sim struct {
	dyn    mapping.Dynamic
	fmax   int
	fields map[string]mapping.Type
	mv     int
	docs   map[string]map[string]any
}

func newSim(dyn mapping.Dynamic, fmax int) *sim {
	return &sim{dyn: dyn, fmax: fmax, fields: map[string]mapping.Type{}, docs: map[string]map[string]any{}}
}

func (s *sim) index(id string, doc map[string]any) (map[string]any, []string, int, error) {
	fields := make(map[string]mapping.Type, len(s.fields)+8)
	for k, v := range s.fields {
		fields[k] = v
	}
	added := false
	var ignored []string
	out, err := s.walk(fields, &added, &ignored, "", doc)
	if err != nil {
		return nil, nil, s.mv, err
	}
	s.fields = fields
	if added {
		s.mv++
	}
	s.docs[id] = out
	sort.Strings(ignored)
	return out, ignored, s.mv, nil
}

func (s *sim) walk(fields map[string]mapping.Type, added *bool, ignored *[]string, prefix string, obj map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(obj))
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		v := obj[k]
		switch x := v.(type) {
		case nil:
			out[k] = nil
		case map[string]any:
			t, ign, err := s.fieldFor(fields, added, ignored, p, mapping.Object)
			if err != nil {
				return nil, err
			}
			if ign {
				continue
			}
			if t != mapping.Object {
				return nil, &mapping.PathError{Path: p, Err: mapping.ErrTypeConflict}
			}
			sub, err := s.walk(fields, added, ignored, p, x)
			if err != nil {
				return nil, err
			}
			out[k] = sub
		case []any:
			var first any
			for _, el := range x {
				if el != nil {
					first = el
					break
				}
			}
			if first == nil {
				cp := make([]any, len(x))
				copy(cp, x)
				out[k] = cp
				continue
			}
			t, ign, err := s.fieldFor(fields, added, ignored, p, simInfer(first))
			if err != nil {
				return nil, err
			}
			if ign {
				continue
			}
			if t == mapping.Object {
				return nil, &mapping.PathError{Path: p, Err: mapping.ErrTypeConflict}
			}
			res := make([]any, len(x))
			for i, el := range x {
				if el == nil {
					continue
				}
				c, err := coerce.Coerce(t, el)
				if err != nil {
					return nil, &mapping.PathError{Path: p, Err: mapping.ErrTypeConflict}
				}
				res[i] = c
			}
			out[k] = res
		default:
			t, ign, err := s.fieldFor(fields, added, ignored, p, simInfer(v))
			if err != nil {
				return nil, err
			}
			if ign {
				continue
			}
			if t == mapping.Object {
				return nil, &mapping.PathError{Path: p, Err: mapping.ErrTypeConflict}
			}
			c, err := coerce.Coerce(t, v)
			if err != nil {
				return nil, &mapping.PathError{Path: p, Err: mapping.ErrTypeConflict}
			}
			out[k] = c
		}
	}
	return out, nil
}

// fieldFor 返回路径的类型；没有字段时按 dynamic 处理。
// ign=true 表示 dynamic=false 忽略。
func (s *sim) fieldFor(fields map[string]mapping.Type, added *bool, ignored *[]string, p string, want mapping.Type) (t mapping.Type, ign bool, err error) {
	if t, has := fields[p]; has {
		return t, false, nil
	}
	switch s.dyn {
	case mapping.False:
		*ignored = append(*ignored, p)
		return 0, true, nil
	case mapping.Strict:
		return 0, false, &mapping.PathError{Path: p, Err: mapping.ErrStrict}
	}
	if len(fields)+1 > s.fmax {
		return 0, false, &mapping.PathError{Path: p, Err: mapping.ErrFieldLimit}
	}
	fields[p] = want
	*added = true
	return want, false, nil
}

func simInfer(v any) mapping.Type {
	switch v.(type) {
	case bool:
		return mapping.Bool
	case int64:
		return mapping.Long
	case float64:
		return mapping.Double
	case string:
		return mapping.Keyword
	}
	return mapping.Object
}

func (s *sim) putMapping(path string, typ mapping.Type) (int, error) {
	segs := strings.Split(path, ".")
	for i := 0; i < len(segs)-1; i++ {
		p := strings.Join(segs[:i+1], ".")
		if t, has := s.fields[p]; has && t != mapping.Object {
			return s.mv, &mapping.PathError{Path: p, Err: mapping.ErrTypeConflict}
		}
	}
	if t, has := s.fields[path]; has {
		if t == typ {
			return s.mv, nil
		}
		return s.mv, &mapping.PathError{Path: path, Err: mapping.ErrTypeConflict}
	}
	newNodes := 0
	for i := 1; i <= len(segs); i++ {
		if _, has := s.fields[strings.Join(segs[:i], ".")]; !has {
			newNodes++
		}
	}
	if len(s.fields)+newNodes > s.fmax {
		return s.mv, &mapping.PathError{Path: path, Err: mapping.ErrFieldLimit}
	}
	for i := 1; i <= len(segs); i++ {
		p := strings.Join(segs[:i], ".")
		if _, has := s.fields[p]; !has {
			if i == len(segs) {
				s.fields[p] = typ
			} else {
				s.fields[p] = mapping.Object
			}
		}
	}
	s.mv++
	return s.mv, nil
}

var keyPool = []string{"a", "b", "c", "d", "e", "x", "y", "z", "k1", "bb"}

var strPool = []string{"1", "2", "-7", "07", "-0", "true", "false", "abc", "", "1.5",
	"9223372036854775807", "9223372036854775808"}

func randValue(r *rand.Rand, depth int) any {
	switch r.Intn(12) {
	case 0:
		return nil
	case 1:
		return r.Intn(2) == 0
	case 2, 3:
		return int64(r.Intn(10))
	case 4:
		return int64(r.Int63())
	case 5:
		return []any{2.0, 2.5, 3.0, -1.5, math.NaN()}[r.Intn(5)]
	case 6:
		return strPool[r.Intn(len(strPool))]
	case 7:
		if depth < 3 {
			return randDoc(r, depth+1)
		}
		return int64(r.Intn(100))
	case 8, 9:
		n := r.Intn(4)
		arr := make([]any, n)
		for i := range arr {
			arr[i] = randScalar(r)
		}
		return arr
	default:
		return int64(r.Intn(100))
	}
}

func randScalar(r *rand.Rand) any {
	switch r.Intn(6) {
	case 0:
		return nil
	case 1:
		return r.Intn(2) == 0
	case 2:
		return int64(r.Intn(10))
	case 3:
		return []any{2.0, 2.5, -0.5}[r.Intn(3)]
	case 4:
		return strPool[r.Intn(len(strPool))]
	default:
		return int64(r.Int63())
	}
}

func randDoc(r *rand.Rand, depth int) map[string]any {
	n := r.Intn(4)
	doc := make(map[string]any, n)
	for i := 0; i < n; i++ {
		doc[keyPool[r.Intn(len(keyPool))]] = randValue(r, depth)
	}
	return doc
}

func randPath(r *rand.Rand) string {
	n := 1 + r.Intn(3)
	segs := make([]string, n)
	for i := range segs {
		segs[i] = keyPool[r.Intn(len(keyPool))]
	}
	return strings.Join(segs, ".")
}

func sameErr(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	for _, s := range []error{mapping.ErrTypeConflict, mapping.ErrStrict, mapping.ErrFieldLimit,
		mapping.ErrInvalidArgument, mapping.ErrDocNotFound} {
		if errors.Is(a, s) != errors.Is(b, s) {
			return false
		}
	}
	var pa, pb *mapping.PathError
	ha, hb := errors.As(a, &pa), errors.As(b, &pb)
	if ha != hb {
		return false
	}
	if ha && pa.Path != pb.Path {
		return false
	}
	return true
}

func errDesc(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// TestRandomSequences 1500 组随机文档序列与朴素模拟逐步对照。
func TestRandomSequences(t *testing.T) {
	const sequences = 1500
	ids := []string{"d1", "d2", "d3"}
	dyns := []mapping.Dynamic{mapping.True, mapping.False, mapping.Strict}
	typs := []mapping.Type{mapping.Long, mapping.Double, mapping.Keyword, mapping.Bool, mapping.Object}

	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		dyn := dyns[r.Intn(3)]
		fmax := 1 + r.Intn(20)
		eng, err := ingest.New(dyn, fmax)
		if err != nil {
			t.Fatal(err)
		}
		sm := newSim(dyn, fmax)
		ops := 3 + r.Intn(8)
		for op := 0; op < ops; op++ {
			step := fmt.Sprintf("seq=%d op=%d dyn=%d fmax=%d", seq, op, dyn, fmax)
			switch r.Intn(10) {
			case 0, 1: // PutMapping
				path := randPath(r)
				typ := typs[r.Intn(len(typs))]
				mvE, errE := eng.PutMapping(path, typ)
				mvS, errS := sm.putMapping(path, typ)
				t.Logf("%s PutMapping(%s, %s) -> engine mv=%d err=%s | sim mv=%d err=%s | 判定: 错误类别+路径与 mv 一致",
					step, path, typ, mvE, errDesc(errE), mvS, errDesc(errS))
				if !sameErr(errE, errS) || mvE != mvS {
					t.Fatalf("%s PutMapping(%s, %s): engine(mv=%d, %v) != sim(mv=%d, %v)",
						step, path, typ, mvE, errE, mvS, errS)
				}
			case 2: // Get
				id := ids[r.Intn(len(ids))]
				docE, errE := eng.Get(id)
				docS, okS := sm.docs[id]
				t.Logf("%s Get(%s) -> engine err=%s | sim 存在=%v | 判定: 存在性与文档一致",
					step, id, errDesc(errE), okS)
				if !okS {
					if !errors.Is(errE, mapping.ErrDocNotFound) {
						t.Fatalf("%s Get(%s): 应报文档不存在: %v", step, id, errE)
					}
				} else {
					if errE != nil || !reflect.DeepEqual(docE, docS) {
						t.Fatalf("%s Get(%s): engine(%v, %v) != sim(%v)", step, id, docE, errE, docS)
					}
				}
			default: // Index
				id := ids[r.Intn(len(ids))]
				doc := randDoc(r, 1)
				nE, igE, mvE, errE := eng.Index(id, doc)
				nS, igS, mvS, errS := sm.index(id, doc)
				t.Logf("%s Index(%s, %v) -> engine mv=%d ignored=%v err=%s | sim mv=%d ignored=%v err=%s | 判定: 错误类别+路径、规范化文档、Ignored、mv 一致",
					step, id, doc, mvE, igE, errDesc(errE), mvS, igS, errDesc(errS))
				if !sameErr(errE, errS) {
					t.Fatalf("%s Index(%s, %v): 错误不一致 engine=%v sim=%v", step, id, doc, errE, errS)
				}
				if errE == nil {
					if mvE != mvS || !reflect.DeepEqual(nE, nS) || !reflect.DeepEqual(igE, igS) {
						t.Fatalf("%s Index(%s, %v): engine(mv=%d doc=%v ign=%v) != sim(mv=%d doc=%v ign=%v)",
							step, id, doc, mvE, nE, igE, mvS, nS, igS)
					}
				}
			}
		}
		// 序列结束：映射与版本整体一致。
		if got := eng.Fields(); !reflect.DeepEqual(got, sm.fields) {
			t.Fatalf("seq=%d 终态映射不一致: engine=%v sim=%v", seq, got, sm.fields)
		}
		if eng.MV() != sm.mv {
			t.Fatalf("seq=%d 终态 mv 不一致: engine=%d sim=%d", seq, eng.MV(), sm.mv)
		}
		for id, docS := range sm.docs {
			docE, err := eng.Get(id)
			if err != nil || !reflect.DeepEqual(docE, docS) {
				t.Fatalf("seq=%d 终态文档 %s 不一致: engine=%v(%v) sim=%v", seq, id, docE, err, docS)
			}
		}
		t.Logf("seq=%d 终态一致: fields=%d mv=%d docs=%d", seq, len(sm.fields), sm.mv, len(sm.docs))
	}
}
