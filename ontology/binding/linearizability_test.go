package binding

import (
	"math/rand"
	"sync"
	"testing"
)

// naiveModel 是刻意朴素、显然正确的串行参考实现：保留每个字段的
// 全部不可变历史版本，可对任意 (左版本, 右版本) 组合复算结论。
// 所有驱动原语（apply / remove）同时作用于真实实现与模型，
// 因此两侧版本号一一对应，可直接比对。
type naiveModel struct {
	history map[string][]fieldVersion
	binding BindingSpec
}

func newNaiveModel(spec BindingSpec) *naiveModel {
	return &naiveModel{history: map[string][]fieldVersion{}, binding: spec}
}

func (m *naiveModel) apply(def FieldDef) {
	k := fieldKey(def.ObjectType, def.Name)
	hist := m.history[k]
	cur := fieldVersion{version: 0}
	if len(hist) > 0 {
		cur = hist[len(hist)-1]
	}
	m.history[k] = append(hist, fieldVersion{
		def: def, version: cur.version + 1, deleted: false,
	})
}

func (m *naiveModel) remove(objectType, name string) {
	k := fieldKey(objectType, name)
	hist := m.history[k]
	cur := fieldVersion{version: 0}
	if len(hist) > 0 {
		cur = hist[len(hist)-1]
	}
	m.history[k] = append(hist, fieldVersion{
		def: cur.def, version: cur.version + 1, deleted: true,
	})
}

func (m *naiveModel) verdictAt(lv, rv int64, dir QueryDirection) Verdict {
	s := m.binding
	l, lok := atVersion(m.history[fieldKey(s.LeftObject, s.LeftField)], lv)
	rr, rok := atVersion(m.history[fieldKey(s.RightObject, s.RightField)], rv)
	if !lok || l.deleted || !rok || rr.deleted {
		return VerdictFieldDeleted
	}
	if !directionAllowed(s.Direction, dir) {
		return VerdictDirectionDenied
	}
	return Check(&l.def, &rr.def, s).Verdict
}

func atVersion(history []fieldVersion, version int64) (fieldVersion, bool) {
	for _, fs := range history {
		if fs.version == version {
			return fs, true
		}
	}
	return fieldVersion{}, false
}

type modelDriver struct {
	r     *Registry
	model *naiveModel
	mu    sync.Mutex
}

func (d *modelDriver) apply(def FieldDef) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.r.UpdateField(def)
	d.model.apply(def)
}

func (d *modelDriver) removeAndRestore(def FieldDef) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.r.DeleteField(def.ObjectType, def.Name)
	d.model.remove(def.ObjectType, def.Name)
	d.r.UpdateField(def)
	d.model.apply(def)
}

// 并发字段变更与绑定查询交织。实现每次查询返回它实际读到的不可变
// 版本对；用朴素模型对该版本对复算，结论必须一致。若实现读到了
// 全序中不存在的中间状态，该比对必然失败。
func TestConcurrentLinearizabilityVsNaive(t *testing.T) {
	r, _ := newTestRegistry(t)
	spec := bijectiveSpec("L")
	if err := r.DeclareBinding(spec); err != nil {
		t.Fatal(err)
	}
	model := newNaiveModel(spec)
	model.apply(*enumField("A", "f", false, "a", "b"))
	model.apply(*enumField("B", "g", false, "x", "y"))
	driver := &modelDriver{r: r, model: model}

	variants := []FieldDef{
		*enumField("A", "f", false, "a", "b"),
		*enumField("A", "f", false, "a"),
		*enumField("A", "f", false, "a", "b", "c"),
		*enumField("A", "f", true, "a", "b"),
		{ObjectType: "A", Name: "f", Type: FieldType{Kind: KindInt},
			Allowed: []Value{{Raw: int64(1)}}},
	}
	rightVariants := []FieldDef{
		*enumField("B", "g", false, "x", "y"),
		*enumField("B", "g", false, "x"),
		*enumField("B", "g", false, "x", "y", "z"),
	}

	type obs struct {
		got, want Verdict
		lv, rv    int64
	}
	var obsMu sync.Mutex
	var observations []obs

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			local := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				driver.apply(variants[local.Intn(len(variants))])
				if local.Intn(25) == 0 {
					driver.removeAndRestore(variants[0])
				}
			}
		}(int64(w + 1))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		local := rand.New(rand.NewSource(99))
		for {
			select {
			case <-stop:
				return
			default:
				driver.apply(rightVariants[local.Intn(len(rightVariants))])
			}
		}
	}()

	for q := 0; q < 6; q++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			local := rand.New(rand.NewSource(seed))
			dirs := []QueryDirection{QLeftToRight, QRightToLeft}
			for {
				select {
				case <-stop:
					return
				default:
				}
				dir := dirs[local.Intn(2)]
				got, err := r.Query("L", dir)
				if err != nil {
					t.Errorf("query error: %v", err)
					return
				}
				driver.mu.Lock()
				want := model.verdictAt(got.LeftVersion, got.RightVersion, dir)
				driver.mu.Unlock()
				obsMu.Lock()
				observations = append(observations, obs{
					got: got.Verdict, want: want,
					lv: got.LeftVersion, rv: got.RightVersion,
				})
				obsMu.Unlock()
			}
		}(int64(100 + q))
	}

	for i := 0; i < 800; i++ {
		driver.apply(variants[i%len(variants)])
		if i%60 == 0 {
			driver.removeAndRestore(rightVariants[0])
		}
	}
	close(stop)
	wg.Wait()

	if len(observations) == 0 {
		t.Fatal("no observations recorded")
	}
	mismatch := 0
	for _, o := range observations {
		if o.got != o.want {
			mismatch++
			if mismatch <= 5 {
				t.Errorf("linearization mismatch: impl=%s model=%s at versions L%d/R%d",
					o.got, o.want, o.lv, o.rv)
			}
		}
	}
	if mismatch > 0 {
		t.Fatalf("%d/%d observations had no corresponding serial state", mismatch, len(observations))
	}
	t.Logf("validated %d concurrent observations against naive serial model", len(observations))
}

// 定向交织：读侧结论只能落在三个串行时刻的结论集合内。
func TestNoIntermediateStateBetweenTwoUpdates(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.DeclareBinding(bijectiveSpec("L")); err != nil {
		t.Fatal(err)
	}
	v1 := *enumField("A", "f", false, "a")
	v2 := *enumField("A", "f", false, "a", "b", "c")
	v3 := *enumField("A", "f", false, "a", "b")

	allowed := map[Verdict]bool{}
	for _, v := range []FieldDef{v1, v2, v3} {
		allowed[Check(&v, enumField("B", "g", false, "x", "y"), bijectiveSpec("L")).Verdict] = true
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				res, err := r.Query("L", QLeftToRight)
				if err != nil || !allowed[res.Verdict] {
					t.Errorf("observed verdict outside serial states: %v err=%v", res.Verdict, err)
					return
				}
			}
		}
	}()
	for i := 0; i < 300; i++ {
		r.UpdateField(v1)
		r.UpdateField(v2)
		r.UpdateField(v3)
	}
	close(stop)
	wg.Wait()
}
