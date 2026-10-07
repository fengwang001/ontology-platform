package ontologytest

import (
	"fmt"
	"math/rand"

	"ontology/ontology"
)

func rix(rng *rand.Rand, n int) int { return rng.Intn(n) }

func (p modelPair) setRegionCode(rng *rand.Rand, n int) {
	id := ID(fmt.Sprintf("R%d", rix(rng, n)))
	code := Val(fmt.Sprintf("RC%d", rix(rng, 3)))
	if _, err := p.st.SetAttribute(oid(id), opn(pCode),
		ontology.PropertyValue{Val: string(code), Has: true}); err != nil {
		panic(fmt.Sprintf("unexpected set error: %v", err))
	}
	p.nm.SetAttr(id, pCode, code, true)
}

func (p modelPair) unsetRegionCode(rng *rand.Rand, n int) {
	id := ID(fmt.Sprintf("R%d", rix(rng, n)))
	if _, err := p.st.SetAttribute(oid(id), opn(pCode),
		ontology.PropertyValue{}); err != nil {
		panic(fmt.Sprintf("unexpected unset error: %v", err))
	}
	p.nm.SetAttr(id, pCode, "", false)
}

// toggleLink 随机增删一条边。lInRegion 允许第二条出边，制造非唯一不可索引状态。
func (p modelPair) toggleLink(rng *rand.Rand, l LinkName, nFrom, nTo int) {
	from := ID(fmt.Sprintf("%s%d", prefix(l), rix(rng, nFrom)))
	to := ID(fmt.Sprintf("%s%d", prefixTarget(l), rix(rng, nTo)))
	// 以朴素模型当前边集合决定增或删。
	exists := p.nm.out[from] != nil && p.nm.out[from][l] != nil && p.nm.out[from][l][to]
	if exists {
		// 删除已存在边必然成功（基数约束不影响删除）。
		if _, err := p.st.RemoveLink(oln(l), oid(from), oid(to)); err != nil {
			panic(fmt.Sprintf("unexpected remove error: %v", err))
		}
		p.nm.RemoveLink(l, from, to)
		return
	}
	_, err := p.st.AddLink(oln(l), oid(from), oid(to))
	if err != nil {
		// 唯一链接基数冲突（KindNotUnique）：朴素模型同样不应用该边。
		if ontology.ErrorKindOf(err) == ontology.KindNotUnique {
			return
		}
		panic(fmt.Sprintf("unexpected add error: %v", err))
	}
	p.nm.AddLink(l, from, to)
}

// concurrentTriple 把“写源属性 / 删旧链接 / 建指向同一城市的新链接”三者
// 作为单个原子单元按随机相对顺序提交（由 Multi 保证串行等价）。
// 无论内部顺序如何，最终状态必须与朴素模型按同一次原子应用的结果一致。
func (p modelPair) concurrentTriple(rng *rand.Rand, nCities, nStores, nRegions int) {
	storeID := ID(fmt.Sprintf("S%d", rix(rng, nStores)))
	cityA := ID(fmt.Sprintf("C%d", rix(rng, nCities)))
	cityB := ID(fmt.Sprintf("C%d", rix(rng, nCities)))
	if cityA == cityB {
		cityB = ID(fmt.Sprintf("C%d", (int(cityB[1]-'0')+1)%nCities))
	}
	region := ID(fmt.Sprintf("R%d", rix(rng, nRegions)))
	newCode := Val(fmt.Sprintf("RC%d", rix(rng, 3)))

	// 建立可应用三者操作的前置真值：cityA -> region、storeID -> cityA。
	// 全部前置通过两侧分别应用同一批操作完成；任何唯一基数冲突都放弃本轮
	// （两侧状态保持一致，不影响后续对拍）。
	setup := p.buildSetupOps(cityA, cityB, storeID, region)
	if setup == nil {
		return
	}
	if _, err := p.st.Multi(setup.stOps); err != nil {
		return // 前置不成立（如基数冲突），两侧保持原状。
	}
	for _, op := range setup.naive {
		op(p.nm)
	}

	ops := []ontology.Op{
		{Kind: ontology.OpSetAttribute, ID: oid(region), Prop: opn(pCode),
			Val: ontology.PropertyValue{Val: string(newCode), Has: true}},
		{Kind: ontology.OpRemoveLink, ID: oid(storeID), Link: oln(lInCity), To: oid(cityA)},
		{Kind: ontology.OpAddLink, ID: oid(storeID), Link: oln(lInCity), To: oid(cityB)},
	}
	rng.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
	if _, err := p.st.Multi(ops); err != nil {
		panic(fmt.Sprintf("unexpected multi error: %v", err))
	}
	// 朴素模型按同一最终效果应用（顺序无关：真值集合的最终形态）。
	p.nm.SetAttr(region, pCode, newCode, true)
	p.nm.RemoveLink(lInCity, storeID, cityA)
	p.nm.AddLink(lInCity, storeID, cityB)
}

type setupResult struct {
	stOps []ontology.Op
	naive []func(*NaiveModel)
}

// buildSetupOps 构造前置真值操作，并保证前置应用后：
//   - cityB 不被任何 store 以 inCity 指向（保证唯一基数，三操作一定可提交）；
//   - storeID 指向 cityA，cityA 指向 region。
func (p modelPair) buildSetupOps(cityA, cityB, storeID, region ID) *setupResult {
	var stOps []ontology.Op
	var naive []func(*NaiveModel)
	// 若 cityB 已被别的 store 指向，放弃本轮，避免唯一基数冲突。
	for from, links := range p.nm.out {
		if from == storeID {
			continue
		}
		if links[lInCity] != nil && links[lInCity][cityB] {
			return nil
		}
	}
	// storeID 清掉既有 inCity 出边后指向 cityA。
	for old := range p.nm.out[storeID][lInCity] {
		if old != cityA {
			stOps = append(stOps, ontology.Op{
				Kind: ontology.OpRemoveLink, ID: oid(storeID),
				Link: oln(lInCity), To: oid(old)})
			oldID := old
			naive = append(naive, func(m *NaiveModel) { m.RemoveLink(lInCity, storeID, oldID) })
		}
	}
	if !p.nm.out[storeID][lInCity][cityA] {
		stOps = append(stOps, ontology.Op{
			Kind: ontology.OpAddLink, ID: oid(storeID),
			Link: oln(lInCity), To: oid(cityA)})
		naive = append(naive, func(m *NaiveModel) { m.AddLink(lInCity, storeID, cityA) })
	}
	// cityA 清掉既有 inRegion 出边后指向 region（inRegion 非唯一基数，无冲突风险，
	// 但为保证朴素真值一致，同样显式重建）。
	for old := range p.nm.out[cityA][lInRegion] {
		if old != region {
			stOps = append(stOps, ontology.Op{
				Kind: ontology.OpRemoveLink, ID: oid(cityA),
				Link: oln(lInRegion), To: oid(old)})
			oldID := old
			naive = append(naive, func(m *NaiveModel) { m.RemoveLink(lInRegion, cityA, oldID) })
		}
	}
	if !p.nm.out[cityA][lInRegion][region] {
		stOps = append(stOps, ontology.Op{
			Kind: ontology.OpAddLink, ID: oid(cityA),
			Link: oln(lInRegion), To: oid(region)})
		naive = append(naive, func(m *NaiveModel) { m.AddLink(lInRegion, cityA, region) })
	}
	return &setupResult{stOps: stOps, naive: naive}
}

func (p modelPair) deleteAndRecreate(rng *rand.Rand, nRegions int) {
	id := ID(fmt.Sprintf("R%d", rix(rng, nRegions)))
	if _, err := p.st.DeleteInstance(oid(id)); err != nil {
		panic(fmt.Sprintf("unexpected delete error: %v", err))
	}
	p.nm.Delete(id)
	// 同 ID 重建，验证下游从不可索引恢复的过程。
	code := Val(fmt.Sprintf("RC%d", rix(rng, 3)))
	if err := p.st.CreateInstance(oid(id), otn(tRegion),
		map[ontology.PropertyName]string{opn(pCode): string(code)}); err != nil {
		panic(fmt.Sprintf("unexpected recreate error: %v", err))
	}
	p.nm.Create(id, tRegion, map[PropName]Val{pCode: code})
}

func prefix(l LinkName) string {
	if l == lInRegion {
		return "C"
	}
	return "S"
}

func prefixTarget(l LinkName) string {
	if l == lInRegion {
		return "R"
	}
	return "C"
}
