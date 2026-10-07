package ontology

// dual 把每个写操作同时应用到 Store 与 Naive（仅当 Store 接受时），
// 供并发测试与随机对照测试复用。
type dual struct {
	s *Store
	n *Naive
}

func newDual() *dual { return &dual{s: NewStore(), n: &Naive{}} }

func (d *dual) defineObjectType(typeID string, props []PropertyDef) (Version, error) {
	v, err := d.s.DefineObjectType(typeID, props)
	if err == nil {
		d.n.DefineObjectType(v, typeID, props)
	}
	return v, err
}

func (d *dual) migrateObjectType(typeID string, add []PropertyDef, drop []string) (Version, error) {
	v, err := d.s.MigrateObjectType(typeID, add, drop)
	if err == nil {
		d.n.MigrateObjectType(v, typeID, add, drop)
	}
	return v, err
}

func (d *dual) defineLinkType(typeID, fromType, toType string, card Cardinality) (Version, error) {
	v, err := d.s.DefineLinkType(typeID, fromType, toType, card)
	if err == nil {
		d.n.DefineLinkType(v, typeID, fromType, toType, card)
	}
	return v, err
}

func (d *dual) adjustCardinality(typeID string, card Cardinality) (Version, error) {
	v, err := d.s.AdjustCardinality(typeID, card)
	if err == nil {
		d.n.AdjustCardinality(v, typeID, card)
	}
	return v, err
}

func (d *dual) putObject(typeID, objID string, props map[string]Value) (Version, error) {
	v, err := d.s.PutObject(typeID, objID, props)
	if err == nil {
		d.n.PutObject(v, typeID, objID, props)
	}
	return v, err
}

func (d *dual) setProperty(objID, propName string, val Value) (Version, error) {
	v, err := d.s.SetProperty(objID, propName, val)
	if err == nil {
		d.n.SetProperty(v, objID, propName, val)
	}
	return v, err
}

func (d *dual) deleteObject(objID string) (Version, error) {
	v, err := d.s.DeleteObject(objID)
	if err == nil {
		d.n.DeleteObject(v, objID)
	}
	return v, err
}

func (d *dual) addLink(typeID, from, to string) (Version, error) {
	v, err := d.s.AddLink(typeID, from, to)
	if err == nil {
		d.n.AddLink(v, typeID, from, to)
	}
	return v, err
}

func (d *dual) removeLink(typeID, from, to string) (Version, error) {
	v, err := d.s.RemoveLink(typeID, from, to)
	if err == nil {
		d.n.RemoveLink(v, typeID, from, to)
	}
	return v, err
}

func (d *dual) compact(keepFrom Version) {
	d.s.Compact(keepFrom)
	d.n.Compact(keepFrom)
}

func (d *dual) declareGap(kind, scope string, from, to Version) {
	d.s.DeclareGap(kind, scope, from, to)
	d.n.DeclareGap(kind, scope, from, to)
}
