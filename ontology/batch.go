package ontology

import (
	"fmt"
	"sort"
)

// BatchImporter 批量导入的属性级权限守门器。
type BatchImporter struct {
	store  *Store
	logger Logger
}

// NewBatchImporter 创建守门器；logger 可为 nil（丢弃日志）。
func NewBatchImporter(store *Store, logger Logger) *BatchImporter {
	return &BatchImporter{store: store, logger: logger}
}

// Execute 执行一次批量导入。
// 全程持有存储锁：批内记录与并发批次均等价于某个全局串行顺序，
// 且权限快照在发起时刻物化，执行期间的权限变更不影响本次判定。
func (b *BatchImporter) Execute(req BatchRequest) BatchResult {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	return b.executeLocked(req, nil)
}

// executeLocked 为 Execute 的可测试内核：hook 在每条记录处理完成后回调，
// 仅测试用于在批执行期间变更权限以验证快照语义。
func (b *BatchImporter) executeLocked(req BatchRequest, hook func(recordIndex int)) BatchResult {
	// 整批级拒绝，优先级：模式参数缺失或非法 > 发起主体不存在。
	if req.Mode != ModeAtomic && req.Mode != ModeLenient {
		reason := fmt.Sprintf("导入模式参数缺失或非法: %q", req.Mode)
		b.log("batch", -1,
			fmt.Sprintf("mode=%q subject=%q records=%d", req.Mode, req.Subject, len(req.Records)),
			"rejected:"+string(FailBatchInvalidParam),
			"模式参数校验优先于主体存在性校验")
		return BatchResult{Rejected: true, RejectReason: reason}
	}
	if !b.store.subjects[req.Subject] {
		reason := fmt.Sprintf("发起主体不存在: %q", req.Subject)
		b.log("batch", -1,
			fmt.Sprintf("mode=%q subject=%q records=%d", req.Mode, req.Subject, len(req.Records)),
			"rejected:"+string(FailBatchInvalidParam),
			"主体不在已注册主体集合中")
		return BatchResult{Rejected: true, RejectReason: reason}
	}

	// 发起时刻物化权限快照；执行期间的权限变更不影响本次判定。
	snap := b.store.snapshotPermissionsLocked()

	result := BatchResult{Results: make([]RecordResult, 0, len(req.Records))}
	for i, rec := range req.Records {
		result.Results = append(result.Results, b.processRecord(req, snap, rec, i))
		if hook != nil {
			hook(i)
		}
	}
	return result
}

// processRecord 按记录级优先级判定单条记录：
// 类型不存在 > 语义不匹配 > 字段权限 > 跳过后必需属性约束。
// 调用方持有存储锁，失败路径不触碰任何已有状态。
func (b *BatchImporter) processRecord(req BatchRequest, snap *PermissionSnapshot, rec Record, index int) RecordResult {
	input := fmt.Sprintf("type=%q id=%q semantic=%q fields=%v", rec.ObjectType, rec.ObjectID, rec.Semantic, rec.Fields)
	fail := func(cat FailureCategory, reason, basis string) RecordResult {
		b.log("record", index, input, "failed:"+string(cat), basis)
		return RecordResult{Index: index, Status: StatusFailed, FailCategory: cat, Reason: reason}
	}

	objType, ok := b.store.types[rec.ObjectType]
	if !ok {
		return fail(FailTypeNotFound,
			fmt.Sprintf("对象类型不存在: %q", rec.ObjectType),
			"记录级优先级首位：类型存在性校验")
	}

	existing, exists := b.store.getObjectLocked(rec.ObjectType, rec.ObjectID)
	switch rec.Semantic {
	case SemanticCreate:
		if exists {
			return fail(FailSemanticMismatch,
				fmt.Sprintf("创建语义但对象已存在: %q/%q", rec.ObjectType, rec.ObjectID),
				"创建语义要求目标对象不存在")
		}
	case SemanticUpdate:
		if !exists {
			return fail(FailSemanticMismatch,
				fmt.Sprintf("更新语义但对象不存在: %q/%q", rec.ObjectType, rec.ObjectID),
				"更新语义要求目标对象已存在")
		}
	default:
		return fail(FailSemanticMismatch,
			fmt.Sprintf("记录语义非法: %q", rec.Semantic),
			"语义必须为 create 或 update")
	}

	// 字段权限判定：基于发起时刻的权限快照，逐字段 O(1) 查询。
	var writable, skipped []string
	for _, field := range sortedKeys(rec.Fields) {
		if snap.Writable(req.Subject, rec.ObjectType, field) {
			writable = append(writable, field)
		} else {
			skipped = append(skipped, field)
		}
	}

	if req.Mode == ModeAtomic && len(skipped) > 0 {
		return fail(FailPermissionDenied,
			fmt.Sprintf("原子模式下存在不可写字段: %v", skipped),
			"原子模式：任一字段不可写则整条失败，不写入任何字段")
	}

	// 必需属性约束（宽松模式跳过后重新判定）：
	// 创建语义无旧值可沿用；更新语义可沿用对象既有值。
	var missing []string
	for _, prop := range objType.RequiredProps {
		if contains(skipped, prop) {
			if rec.Semantic == SemanticUpdate {
				if _, hasOld := existing.Props[prop]; hasOld {
					continue
				}
			}
			missing = append(missing, prop)
			continue
		}
		if _, incoming := rec.Fields[prop]; incoming {
			continue
		}
		if rec.Semantic == SemanticUpdate {
			if _, hasOld := existing.Props[prop]; hasOld {
				continue
			}
		}
		missing = append(missing, prop)
	}
	if len(missing) > 0 {
		return fail(FailRequiredConstraint,
			fmt.Sprintf("必需属性缺失: %v", missing),
			"跳过后重新判定必需属性约束，且无既有值可沿用")
	}

	// 通过全部判定，落库可写字段。
	target, ok := b.store.getObjectLocked(rec.ObjectType, rec.ObjectID)
	if !ok {
		target = Object{TypeName: rec.ObjectType, ID: rec.ObjectID, Props: map[string]string{}}
	}
	for _, field := range writable {
		target.Props[field] = rec.Fields[field]
	}
	b.store.putObjectLocked(target)

	if len(skipped) > 0 {
		b.log("record", index, input, "partial_success",
			fmt.Sprintf("宽松模式跳过不可写字段 %v，必需属性约束仍满足", skipped))
		return RecordResult{Index: index, Status: StatusPartial, Skipped: skipped}
	}
	b.log("record", index, input, "success", "全部字段可写且必需属性约束满足")
	return RecordResult{Index: index, Status: StatusSuccess}
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func (b *BatchImporter) log(scope string, index int, input, output, basis string) {
	if b.logger == nil {
		return
	}
	b.logger.LogDecision(DecisionLog{Scope: scope, Index: index, Input: input, Output: output, Basis: basis})
}

// sortedKeys 返回排序后的键，保证日志与结果确定。
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
