package ontology

// Item 是集合中的对象：只有字符串主键与一个 int 字段。
// 集合按 Key 的字典序排序。
type Item struct {
	Key   string
	Value int
}

// ChangeInfo 报告一次遍历（会话）自创建以来集合发生过的变更。
//
// 插入与删除分别统计、分别置位：Inserted 表示出现过排序位置位于
// “尚未翻到”区域的新元素；Deleted 表示在首次 Scan 时已存在、
// 但在翻到它之前被删除的元素（属于“丢弃”而不是“截断”）。
type ChangeInfo struct {
	Inserted      bool
	InsertedCount int
	Deleted       bool
	DeletedCount  int
}

// Page 是一次 Scan 的结果。
type Page struct {
	Items []Item

	// NextCursor 是不透明字符串，指向下一页的确切续点；
	// 没有更多内容时与传入游标表示同一位置（可用它继续得到空页）。
	NextCursor string

	// HasMore 表示是否因为 limit 而在还有元素处被“截断”。
	HasMore bool

	// Dropped 是本页中因遍历期间被删除而“丢弃”的元素数
	//（这些元素在快照里存在、已不属于截断的剩余内容）。
	Dropped int

	// truncated 是最近一次取页时因 limit 截断而留在游标后的快照元素数。
	// 包内字段：供 Stats 读取，不属于公开 API。
	truncated int

	// Changes 是截至本次 Scan 时会话累计的集合变更标记。
	Changes ChangeInfo
}

// SkipStats 是一次遍历累计的跳过统计，跳过原因分类计数。
type SkipStats struct {
	// Truncated 是因 limit 截断而留在游标之后的元素数（最近一次实际取页时）。
	Truncated int
	// Deleted 是因遍历期间被删除而丢弃的、快照中原有的元素数（累计）。
	Deleted int
	// Inserted 是排序位置落在尚未翻到区域、不会出现在本次遍历中的新元素数（累计）。
	Inserted int
}
