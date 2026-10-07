package ontology

// ImportMode 是批量导入的整体语义，调用时必须恰好声明一种。
type ImportMode int

const (
	// ModeAllOrNothing 全有或全无：任一条目失败则整批撤销。
	ModeAllOrNothing ImportMode = iota
	// ModeBestEffort 尽力而为：失败条目跳过，其余照常生效。
	ModeBestEffort
)

// ItemStatus 是单条输入在批次结果中的最终状态。
type ItemStatus string

const (
	StatusAccepted   ItemStatus = "accepted"
	StatusRejected   ItemStatus = "rejected"
	StatusRolledBack ItemStatus = "rolled_back"
	StatusSkipped    ItemStatus = "skipped"
)

// InstancePair 是一条待导入链接的有序实例对。
type InstancePair struct {
	Source InstanceID
	Target InstanceID
}

// ItemResult 是一条输入的最终处置，报告顺序与输入列表顺序严格一致。
type ItemResult struct {
	Index  int
	Pair   InstancePair
	Status ItemStatus
	Err    *LinkError
}

// BatchReport 是整批导入的完整报告。
type BatchReport struct {
	Mode      ImportMode
	Committed bool
	Results   []ItemResult
	Accepted  int
}

// Importer 负责批量导入的分批提交与回退。
// 整个批次在 Ledger 的同一临界区内执行：批内按列表顺序逐条校验，前面接受
// 的条目立即计入后续条目的基数占用；批次对外部并发操作表现为一个不可分割
// 的全局串行操作；全有或全无需撤销时直接删除本批次已接受的有序对，账本
// 精确回到批次开始前的状态，不存在部分泄漏。
type Importer struct {
	ledger *Ledger
}

func NewImporter(ledger *Ledger) *Importer {
	return &Importer{ledger: ledger}
}

// Import 按声明的整体语义导入一批链接，返回逐条结果的完整清单。
func (im *Importer) Import(linkType LinkTypeName, pairs []InstancePair, mode ImportMode) BatchReport {
	results := make([]ItemResult, len(pairs))
	for i, pair := range pairs {
		results[i] = ItemResult{Index: i, Pair: pair, Status: StatusSkipped}
	}

	im.ledger.lock()
	defer im.ledger.unlock()

	if mode == ModeBestEffort {
		seen := make(map[InstancePair]int, len(pairs))
		accepted := 0
		for i, pair := range pairs {
			err := im.validateInBatch(linkType, pair, seen)
			if err != nil {
				results[i] = ItemResult{Index: i, Pair: pair, Status: StatusRejected, Err: err}
				continue
			}
			im.ledger.applyCreate(linkType, pair.Source, pair.Target)
			seen[pair] = i
			accepted++
			results[i] = ItemResult{Index: i, Pair: pair, Status: StatusAccepted}
		}
		return BatchReport{Mode: mode, Committed: true, Results: results, Accepted: accepted}
	}

	seen := make(map[InstancePair]int, len(pairs))
	acceptedPairs := make([]InstancePair, 0, len(pairs))
	failed := -1
	for i, pair := range pairs {
		err := im.validateInBatch(linkType, pair, seen)
		if err != nil {
			results[i] = ItemResult{Index: i, Pair: pair, Status: StatusRejected, Err: err}
			failed = i
			break
		}
		im.ledger.applyCreate(linkType, pair.Source, pair.Target)
		seen[pair] = i
		acceptedPairs = append(acceptedPairs, pair)
		results[i] = ItemResult{Index: i, Pair: pair, Status: StatusAccepted}
	}

	if failed >= 0 {
		for j := len(acceptedPairs) - 1; j >= 0; j-- {
			pair := acceptedPairs[j]
			key := LinkKey{Type: linkType, Source: pair.Source, Target: pair.Target}
			delete(im.ledger.links, key)
			im.ledger.decCount(linkType, sideSource, pair.Source)
			im.ledger.decCount(linkType, sideTarget, pair.Target)
			results[j].Status = StatusRolledBack
			results[j].Err = nil
		}
		for i := failed + 1; i < len(results); i++ {
			results[i].Status = StatusSkipped
		}
		return BatchReport{Mode: mode, Committed: false, Results: results, Accepted: 0}
	}

	return BatchReport{Mode: mode, Committed: true, Results: results, Accepted: len(acceptedPairs)}
}

// validateInBatch 复用账本的统一校验链，并额外加入「有序对在本批次内重复」
// 检查。错误优先级与单条创建完全一致：参数非法（实例不存在 / 批内重复 /
// 已存在）→ 起点超限 → 终点超限。调用方必须持有账本锁。
func (im *Importer) validateInBatch(linkType LinkTypeName, pair InstancePair, seen map[InstancePair]int) *LinkError {
	entry, ok := im.ledger.linkTypes[linkType]
	if !ok {
		return invalidArgument("link type %q is not registered", linkType)
	}
	if !im.ledger.instanceExists(entry.spec.SourceType, pair.Source) {
		return invalidArgument("source instance %q (type %q) does not exist", pair.Source, entry.spec.SourceType)
	}
	if !im.ledger.instanceExists(entry.spec.TargetType, pair.Target) {
		return invalidArgument("target instance %q (type %q) does not exist", pair.Target, entry.spec.TargetType)
	}
	if _, dup := seen[pair]; dup {
		return invalidArgument("ordered pair (%q,%q) is duplicated within this batch", pair.Source, pair.Target)
	}
	key := LinkKey{Type: linkType, Source: pair.Source, Target: pair.Target}
	if _, dup := im.ledger.links[key]; dup {
		return invalidArgument("ordered pair (%q,%q) is already linked by %q", pair.Source, pair.Target, linkType)
	}
	if entry.sourceCap {
		cur := im.ledger.counts[countKey{linkType, sideSource, pair.Source}]
		if cur >= entry.sourceLimit {
			return sourceCapExceeded(
				"source %q already occupies %d/%d of link type %q",
				pair.Source, cur, entry.sourceLimit, linkType)
		}
	}
	if entry.targetCap {
		cur := im.ledger.counts[countKey{linkType, sideTarget, pair.Target}]
		if cur >= entry.targetLimit {
			return targetCapExceeded(
				"target %q already occupies %d/%d of link type %q",
				pair.Target, cur, entry.targetLimit, linkType)
		}
	}
	return nil
}
