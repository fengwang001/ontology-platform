package ontology

import (
	"sync"
	"sync/atomic"
)

// session 是一次遍历（扫描会话）的全部状态，仅存在于进程内存。
//
// snapshot 是首次 Scan 时刻集合主键排序后的拷贝，提供快照语义：
// 翻页期间的插入不会进入已存在的遍历，删除则表现为元素丢弃。
type session struct {
	id     []byte
	secret []byte

	// 快照，创建后只读。
	snapshot []string

	mu sync.Mutex
	// position 是快照中“下一个候选元素”的下标，只向前走。
	position int64
	// truncated 是最近一次实际取页时，因 limit 截断而留在游标后的元素数。
	truncated int
	// cache 按起始位置与 limit 缓存已算出的页，保证同一游标重复/并发
	// Scan 返回完全一致的结果，且游标位置不会被重复推进。
	cache map[pageKey]*pageResult

	// 变更标记：inserts/deletes 是全量计数（用于标记是否发生过变更），
	// 只做原子加，不持有任何互斥锁，绝不阻塞写入。
	inserts atomic.Int64
	deletes atomic.Int64
	// insertsAhead / deletesAhead 记录真正落在“尚未翻到”区域、
	// 因而被本次遍历跳过的增删数（跳过原因分类统计用）。
	insertsAhead atomic.Int64
	deletesAhead atomic.Int64
}

type pageKey struct {
	position int64
	limit    int
}

// pageResult 是会话内部缓存的取页结果（游标串由 Scan 层拼装）。
type pageResult struct {
	items     []Item
	nextPos   int64
	hasMore   bool
	dropped   int
	truncated int
}

func newSession(id, secret []byte, snapshot []string) *session {
	return &session{
		id:       id,
		secret:   secret,
		snapshot: snapshot,
		cache:    make(map[pageKey]*pageResult),
	}
}

func (s *session) changeInfo() ChangeInfo {
	ins := s.inserts.Load()
	del := s.deletes.Load()
	return ChangeInfo{
		Inserted:      ins > 0,
		InsertedCount: int(ins),
		Deleted:       del > 0,
		DeletedCount:  int(del),
	}
}

func (s *session) skipStats(truncated int) SkipStats {
	return SkipStats{
		Truncated: truncated,
		Deleted:   int(s.deletesAhead.Load()),
		Inserted:  int(s.insertsAhead.Load()),
	}
}
