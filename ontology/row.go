package ontology

// ColumnValue 是 Ordered 返回的逐列结果。
type ColumnValue struct {
	Column ColumnDef
	Value  string
	Source ValueSource
}

// newRow 构造一个对齐 schema（当前最新结构）的空结果行。
func newRow(schema *Schema) Row {
	return Row{
		SchemaVersion: schema.Version,
		Order:         append([]ColumnID(nil), columnIDs(schema)...),
		schema:        schema,
		values:        make(map[ColumnID]string, len(schema.Columns)),
		sources:       make(map[ColumnID]ValueSource, len(schema.Columns)),
	}
}

// columnIDs 按顺序取出 schema 各列 ID。
func columnIDs(schema *Schema) []ColumnID {
	ids := make([]ColumnID, len(schema.Columns))
	for i, c := range schema.Columns {
		ids[i] = c.ID
	}
	return ids
}

// set 记录一列的解码值与来源。
func (r Row) set(id ColumnID, value string, source ValueSource) {
	r.values[id] = value
	r.sources[id] = source
}

// Get 按列的稳定标识取值。
//   - 第二返回值 source 标识值来自事件（SourceEvent，含空串）
//     还是当前结构的默认值（SourceDefault）；
//   - 第三返回值 ok 表示该 ID 是否属于当前最新结构。
func (r Row) Get(id ColumnID) (value string, source ValueSource, ok bool) {
	if r.sources == nil {
		return "", SourceUnknown, false
	}
	source, ok = r.sources[id]
	if !ok {
		return "", SourceUnknown, false
	}
	return r.values[id], source, true
}

// Value 按列的稳定标识取值；ID 不属于当前结构时返回空串与 false。
// 注意：属于当前结构且值为空串时返回 ("", true)，与“不属于该结构”不同。
func (r Row) Value(id ColumnID) (string, bool) {
	v, _, ok := r.Get(id)
	return v, ok
}

// Present 报告当前结构中该 ID 是否存在。
func (r Row) Present(id ColumnID) bool {
	_, _, ok := r.Get(id)
	return ok
}

// SourceOf 返回该列取值的来源；ID 不属于当前结构时为 SourceUnknown。
func (r Row) SourceOf(id ColumnID) ValueSource {
	_, s, _ := r.Get(id)
	return s
}

// Ordered 按当前结构列顺序返回 (列定义, 值, 来源)，
// 便于以“当前最新结构一行”的形式消费结果。
func (r Row) Ordered() []ColumnValue {
	if r.sources == nil {
		return nil
	}
	// 从 Order 还原列定义（名字/默认值）需要最新结构；Row 不保存
	// 完整列定义，因此这里仅回填 ID 与值，列的 Name/Default 由
	// 解码时通过 Registry 填充。
	out := make([]ColumnValue, 0, len(r.Order))
	for _, id := range r.Order {
		source, ok := r.sources[id]
		if !ok {
			continue
		}
		cv := ColumnValue{
			Column: ColumnDef{ID: id},
			Value:  r.values[id],
			Source: source,
		}
		// 从解码时固定的最新结构快照还原列名与默认值。
		if col, exists := r.schema.Column(id); exists {
			cv.Column = col
		}
		out = append(out, cv)
	}
	return out
}
