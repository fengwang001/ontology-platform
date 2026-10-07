package ontologytest

import (
	"fmt"
	"math/rand"

	"ontology/ontology"
)

// DifferentialConfig 控制对拍场景。
type DifferentialConfig struct {
	Seed       int64
	Iterations int
	// Logger 在每次变更后打印输入、受影响下游集合与判定依据（生产侧日志）。
	Logger interface{ Write(p []byte) (int, error) }
}

const (
	tRegion TypeName = "Region"
	tCity   TypeName = "City"
	tStore  TypeName = "Store"

	pCode   PropName = "code"
	pCityOf PropName = "cityOf"
	pRegion PropName = "regionCode"

	lInRegion LinkName = "inRegion" // City -> Region, unique
	lInCity   LinkName = "inCity"   // Store -> City, unique
)

// BuildSchema 返回一套同时包含单层与两级传递的 schema（生产 + 朴素两份）。
func BuildSchema() (*ontology.Schema, *NaiveModel) {
	s := ontology.NewSchema()
	must(s.RegisterObjectType(otn(tRegion), []ontology.PropertyName{opn(pCode)}))
	must(s.RegisterObjectType(otn(tCity), []ontology.PropertyName{opn(pCode), opn(pRegion)}))
	must(s.RegisterObjectType(otn(tStore), []ontology.PropertyName{opn(pCityOf)}))
	// inRegion 链接本身允许一城市连多区域；但 City.regionCode 派生索引声明要求唯一，
	// 出现多条出边时必须进入 StateNotUnique，而不是任选一个区域。
	must(s.RegisterLinkType(ontology.LinkType{Name: oln(lInRegion), SrcType: otn(tCity), DstType: otn(tRegion), Unique: false}))
	must(s.RegisterLinkType(ontology.LinkType{Name: oln(lInCity), SrcType: otn(tStore), DstType: otn(tCity), Unique: true}))
	// City.regionCode <- inRegion -> Region.code（单层）
	must(s.RegisterDerivedIndex(ontology.DerivedIndex{
		SrcType: otn(tCity), PropName: opn(pRegion), Link: oln(lInRegion), DstProp: opn(pCode)}))
	// Store.cityOf <- inCity -> City.regionCode（第二级，多级传递）
	must(s.RegisterDerivedIndex(ontology.DerivedIndex{
		SrcType: otn(tStore), PropName: opn(pCityOf), Link: oln(lInCity), DstProp: opn(pRegion)}))

	n := NewNaiveModel()
	n.RegisterType(tRegion, []PropName{pCode})
	n.RegisterType(tCity, []PropName{pCode, pRegion})
	n.RegisterType(tStore, []PropName{pCityOf})
	n.RegisterLink(LinkSpec{Name: lInRegion, From: tCity, To: tRegion, Unique: false})
	n.RegisterLink(LinkSpec{Name: lInCity, From: tStore, To: tCity, Unique: true})
	n.RegisterDerived(DerivedSpec{OnType: tCity, Prop: pRegion, Link: lInRegion, Source: pCode})
	n.RegisterDerived(DerivedSpec{OnType: tStore, Prop: pCityOf, Link: lInCity, Source: pRegion})
	return s, n
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type modelPair struct {
	st *ontology.Store
	nm *NaiveModel
}

// RunDifferential 运行随机操作序列并在每个原子单元后对拍全量索引与逐实例状态。
func RunDifferential(cfg DifferentialConfig) error {
	rng := rand.New(rand.NewSource(cfg.Seed))
	schema, nm := BuildSchema()
	st := ontology.NewStore(schema)
	if cfg.Logger != nil {
		st.Journal().SetOutput(cfg.Logger)
	}
	p := modelPair{st: st, nm: nm}

	const nRegions, nCities, nStores = 3, 4, 6
	for i := 0; i < nRegions; i++ {
		id := ID(fmt.Sprintf("R%d", i))
		code := Val(fmt.Sprintf("RC%d", i%2)) // 允许重码，制造多桶命中
		if err := st.CreateInstance(oid(id), otn(tRegion),
			map[ontology.PropertyName]string{opn(pCode): string(code)}); err != nil {
			return err
		}
		nm.Create(id, tRegion, map[PropName]Val{pCode: code})
	}
	for i := 0; i < nCities; i++ {
		id := ID(fmt.Sprintf("C%d", i))
		if err := st.CreateInstance(oid(id), otn(tCity), nil); err != nil {
			return err
		}
		nm.Create(id, tCity, nil)
	}
	for i := 0; i < nStores; i++ {
		id := ID(fmt.Sprintf("S%d", i))
		if err := st.CreateInstance(oid(id), otn(tStore), nil); err != nil {
			return err
		}
		nm.Create(id, tStore, nil)
	}
	if err := p.assertAll(0); err != nil {
		return err
	}

	for step := 1; step <= cfg.Iterations; step++ {
		switch rng.Intn(6) {
		case 0:
			p.setRegionCode(rng, nRegions)
		case 1:
			p.toggleLink(rng, lInRegion, nCities, nRegions)
		case 2:
			p.toggleLink(rng, lInCity, nStores, nCities)
		case 3:
			p.concurrentTriple(rng, nCities, nStores, nRegions)
		case 4:
			p.deleteAndRecreate(rng, nRegions)
		case 5:
			p.unsetRegionCode(rng, nRegions)
		}
		if err := p.assertAll(step); err != nil {
			return fmt.Errorf("step %d: %w", step, err)
		}
	}
	return nil
}

// RunDifferentialSeed 是便捷入口。
func RunDifferentialSeed(seed int64, iterations int) error {
	return RunDifferential(DifferentialConfig{Seed: seed, Iterations: iterations})
}
