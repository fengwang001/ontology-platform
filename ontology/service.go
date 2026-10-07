package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Service 是链接基数约束与批量导入原子性子系统的入口。
//
// 并发模型：一把互斥锁串行化全部对账本/存储的变更（创建、删除、两种批量导入）。
// 因此任意实际执行都严格等价于某个全局串行顺序；删除在释放计数后才退出临界区，
// 后续获取锁的创建立即可见释放出的名额，无额外时延。整批导入在一次锁内完成，
// 全有或全无模式下外部永远观察不到中间态。
type Service struct {
	mu sync.Mutex

	linkTypes map[string]LinkTypeDecl
	objects   map[string]string // 实例ID -> 对象类型ID
	store     *linkStore
	ledger    *ledger
	probe     *probeCounter
}

// NewService 创建空平台服务。
func NewService() *Service {
	return &Service{
		linkTypes: map[string]LinkTypeDecl{},
		objects:   map[string]string{},
		store:     newLinkStore(),
		ledger:    newLedger(),
	}
}

// newServiceWithProbe 创建带校验成本探针的服务（测试专用）：
// 每次 ledger.check 访问计数器都会被探针累计。
func newServiceWithProbe(p *probeCounter) *Service {
	s := NewService()
	s.probe = p
	s.ledger = newLedgerWithProbe(p)
	return s
}

// RegisterLinkType 注册链接类型声明（含两端基数上限）。
func (s *Service) RegisterLinkType(decl LinkTypeDecl) error {
	if err := decl.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.linkTypes[decl.Name]; ok {
		return fmt.Errorf("ontology: 链接类型 %s 已存在", decl.Name)
	}
	s.linkTypes[decl.Name] = decl
	return nil
}

// RegisterObject 注册一个具体实例及其对象类型。
func (s *Service) RegisterObject(id, objectType string) error {
	if id == "" || objectType == "" {
		return fmt.Errorf("ontology: 实例ID与对象类型均不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.objects[id]; ok && existing != objectType {
		return fmt.Errorf("ontology: 实例 %s 已注册为不同对象类型", id)
	}
	s.objects[id] = objectType
	return nil
}

// validateArguments 执行参数非法检查（拒绝次序中最优先）。
// seenPairs 为 nil 表示单条创建；非 nil 表示批次内检查（同时拦截批内重复）。
// 调用方必须持有 s.mu。
func (s *Service) validateArguments(item BatchItem, seenPairs map[Pair]struct{}) (LinkTypeDecl, error) {
	decl, ok := s.linkTypes[item.LinkType]
	if !ok {
		return LinkTypeDecl{}, opError(KindInvalidArgument, "链接类型不存在: "+item.LinkType)
	}
	srcType, srcOK := s.objects[item.Source]
	tgtType, tgtOK := s.objects[item.Target]
	if !srcOK || !tgtOK {
		return LinkTypeDecl{}, opError(KindInvalidArgument, fmt.Sprintf("实例不存在: %q 或 %q", item.Source, item.Target))
	}
	if srcType != decl.SourceType || tgtType != decl.TargetType {
		return LinkTypeDecl{}, opError(KindInvalidArgument, fmt.Sprintf("实例对象类型与链接类型 %s 两端声明不符", item.LinkType))
	}
	if s.store.exists(item.LinkType, item.Pair) {
		return LinkTypeDecl{}, opError(KindInvalidArgument, "同一有序实例对的链接已存在")
	}
	if seenPairs != nil {
		if _, dup := seenPairs[item.Pair]; dup {
			return LinkTypeDecl{}, opError(KindInvalidArgument, "同一有序实例对在批次输入中重复")
		}
	}
	return decl, nil
}

// CreateLink 创建单条链接。
//
// 拒绝次序：参数非法 → 起点一侧超限 → 终点一侧超限，只返回第一个命中原因。
// 两端各自独立检查（即使起点已超限，仍完成终点检查的判定），但任一端超限
// 都不写入任何数据。
func (s *Service) CreateLink(linkType string, p Pair) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := BatchItem{LinkType: linkType, Pair: p}
	decl, err := s.validateArguments(item, nil)
	if err != nil {
		return err
	}
	sourceExceeded, targetExceeded := s.ledger.check(decl, p)
	if sourceExceeded {
		return opError(KindSourceCardinality, fmt.Sprintf("起点实例 %s 在链接类型 %s 上超出基数上限", p.Source, linkType))
	}
	if targetExceeded {
		return opError(KindTargetCardinality, fmt.Sprintf("终点实例 %s 在链接类型 %s 上超出基数上限", p.Target, linkType))
	}
	if !s.store.add(linkType, p) {
		return opError(KindInvalidArgument, "同一有序实例对的链接已存在")
	}
	s.ledger.apply(linkType, p)
	return nil
}

// DeleteLink 删除单条链接。
//
// 删除在同一临界区内同时完成存储移除与两端计数释放，释放对后续获取锁的
// 创建立即可见；链接不存在归一化为 KindNotFound，与基数已满严格区分。
func (s *Service) DeleteLink(linkType string, p Pair) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.linkTypes[linkType]; !ok {
		return opError(KindNotFound, "链接类型不存在: "+linkType)
	}
	if !s.store.remove(linkType, p) {
		return opError(KindNotFound, "链接不存在")
	}
	s.ledger.release(linkType, p)
	return nil
}

// ImportLinks 按声明的整体语义执行批量导入。
//
// 按列表顺序逐条校验/生效：前面已被本次批量接受的条目立即累加进同一本
// 基数账本，后续条目据此判断（批内累积，而非基于批次前快照）。
//
//   - AllOrNothing：第一条失败即中止，已接受的中间态全部按序回滚，
//     Results 长度与输入一致：接受项为 OK，第一个失败项给出原因，
//     其后未处理项标记为 KindInvalidArgument（"批次已中止、未处理"）。
//     整批结束后账本与存储回到批次开始前状态。
//   - BestEffort：逐条给出接受/拒绝及原因，失败条目不占用任何基数、
//     不留存储痕迹，且不影响后续条目；顺序与输入一致。
func (s *Service) ImportLinks(mode BatchMode, items []BatchItem) *BatchResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := &BatchResult{Results: make([]ItemResult, len(items))}
	seenPairs := map[Pair]struct{}{}
	var accepted []int // 已接受条目的下标，供全有或全无回滚

	for i, item := range items {
		decl, err := s.validateArguments(item, seenPairs)
		if err == nil {
			sourceExceeded, targetExceeded := s.ledger.check(decl, item.Pair)
			switch {
			case sourceExceeded:
				err = opError(KindSourceCardinality, "批内起点一侧基数超限")
			case targetExceeded:
				err = opError(KindTargetCardinality, "批内终点一侧基数超限")
			}
		}
		if err != nil {
			res.Results[i] = ItemResult{Accepted: false, Kind: KindOf(err)}
			if mode == AllOrNothing {
				s.rollback(items, accepted)
				for j := i + 1; j < len(items); j++ {
					res.Results[j] = ItemResult{Accepted: false, Kind: KindInvalidArgument}
				}
				res.Aborted = true
				res.AbortIndex = i
				return res
			}
			continue
		}

		// 校验通过：同临界区落存储与账本（add 在防御意义上不可能失败）。
		s.store.add(item.LinkType, item.Pair)
		s.ledger.apply(item.LinkType, item.Pair)
		seenPairs[item.Pair] = struct{}{}
		accepted = append(accepted, i)
		res.Results[i] = ItemResult{Accepted: true, Kind: KindOK}
	}
	return res
}

// rollback 撤销全有或全无批次中已接受的中间态。
// 释放顺序与接受顺序相反；存储删除与计数释放成对发生。
func (s *Service) rollback(items []BatchItem, accepted []int) {
	for j := len(accepted) - 1; j >= 0; j-- {
		item := items[accepted[j]]
		s.store.remove(item.LinkType, item.Pair)
		s.ledger.release(item.LinkType, item.Pair)
	}
}

// Snapshot 返回当前链接集合（按稳定顺序排序），供对账与重放验证。
func (s *Service) Snapshot() []StoredLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.store.snapshot()
	sort.Slice(out, func(i, j int) bool {
		if out[i].LinkType != out[j].LinkType {
			return out[i].LinkType < out[j].LinkType
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Target < out[j].Target
	})
	return out
}

// UsedSource / UsedTarget 暴露实例当前占用，供测试与账本一致性断言。
func (s *Service) UsedSource(linkType, instance string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledger.usedSource(linkType, instance)
}

func (s *Service) UsedTarget(linkType, instance string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledger.usedTarget(linkType, instance)
}
