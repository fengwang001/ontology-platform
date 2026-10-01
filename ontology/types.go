package ontology

// Doc 是登记批次中的一篇文档：键与按出现顺序排列的词项序列（可为空）。
type Doc struct {
	Key   string
	Terms []string
}

// Stats 是一个段的统计量。
type Stats struct {
	MaxDoc    int // 局部编号总数（含已删除）
	NumDocs   int // 存活文档数
	TermCount int // 存活文档中出现过的不同词项数
}

// Posting 是某词项在一篇存活文档中的倒排记录。
type Posting struct {
	DocID     int   // 段内局部编号
	TF        int   // 词项在该文档中的出现次数
	Positions []int // 出现位置（下标），升序
}
