package scheduler

import (
	"io"
	"log/slog"
	"sort"
	"sync"
)

// MaxTransactions 是单个调度器可接受的事务总数上限。
const MaxTransactions = 100_000

// Transaction 描述按提交顺序到达的一个事务。
type Transaction struct {
	Seq    int
	Reads  []string
	Writes map[string]string
}

// accepted 保存一个已接受事务的规范化快照。
type accepted struct {
	seq     int
	reads   []string
	writeKV map[string]string
	keys    []string
	depth   int
	basis   []int
}

// Round 是一次分批调度的结果。
type Round struct {
	Index       int
	Transaction []int
}

// ReplayResult 是回放结果与自检结论。
type ReplayResult struct {
	State       map[string]string
	Rounds      []Round
	Depth       map[int]int
	SelfCheckOK bool
}

// Scheduler 接收事务并提供深度、调度、回放查询。
type Scheduler struct {
	mu     sync.RWMutex
	txs    []*accepted
	bySeq  map[int]*accepted
	logger *slog.Logger
}

// New 创建空调度器。
func New(w io.Writer) *Scheduler {
	var logger *slog.Logger
	if w == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	} else {
		logger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return &Scheduler{bySeq: map[int]*accepted{}, logger: logger}
}

// Add 原子地接收一批连续序号的事务；任一非法则整体拒绝。
func (s *Scheduler) Add(txs []Transaction) error {
	if txs == nil {
		return reject(ReasonNilTransactions, "transactions slice must not be nil")
	}
	if len(txs) == 0 {
		return reject(ReasonEmptyTransactions, "at least one transaction is required")
	}

	s.mu.RLock()
	currentCount := len(s.txs)
	s.mu.RUnlock()
	if currentCount > MaxTransactions-len(txs) {
		return reject(ReasonTooManyTransactions, "accepted transaction count would exceed MaxTransactions")
	}

	parsed := make([]*accepted, len(txs))
	nextSeq := currentCount + 1
	for i, tx := range txs {
		if tx.Seq != nextSeq+i {
			return reject(ReasonSequenceGap, "transaction sequence must start at 1 and increment by 1")
		}
		if len(tx.Writes) == 0 {
			return reject(ReasonEmptyWriteSet, "write set must contain at least one key")
		}
		keys := make([]string, 0, len(tx.Writes))
		seen := map[string]struct{}{}
		for key := range tx.Writes {
			if key == "" {
				return reject(ReasonEmptyWriteKey, "write set must not contain empty keys")
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		writeKV := make(map[string]string, len(keys))
		for _, key := range keys {
			writeKV[key] = tx.Writes[key]
		}
		var reads []string
		if len(tx.Reads) > 0 {
			reads = append(reads, tx.Reads...)
		}
		parsed[i] = &accepted{
			seq:     tx.Seq,
			reads:   reads,
			writeKV: writeKV,
			keys:    keys,
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.txs) != currentCount {
		currentCount = len(s.txs)
		nextSeq = currentCount + 1
		for i, tx := range txs {
			if tx.Seq != nextSeq+i {
				return reject(ReasonSequenceGap, "transaction sequence must start at 1 and increment by 1")
			}
		}
		if currentCount > MaxTransactions-len(txs) {
			return reject(ReasonTooManyTransactions, "accepted transaction count would exceed MaxTransactions")
		}
	}

	for _, item := range parsed {
		depth := 1
		var basis []int
		for _, prior := range s.txs {
			if intersects(prior.keys, item.keys) {
				basis = append(basis, prior.seq)
				if prior.depth+1 > depth {
					depth = prior.depth + 1
				}
			}
		}
		item.depth = depth
		item.basis = basis
		s.txs = append(s.txs, item)
		s.bySeq[item.seq] = item
	}

	for _, item := range parsed {
		s.logger.Info("accepted transaction",
			"seq", item.seq,
			"reads", item.reads,
			"writes", item.keys,
			"depth", item.depth,
			"depth_basis", item.basis,
		)
	}
	return nil
}

func intersects(a, b []string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			return true
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return false
}
