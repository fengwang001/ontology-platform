// Package promote 实现逐级晋级闸门：按固定拒绝次序检查驻留、
// 证明与落标签规则，把源级标签指向的摘要晋到紧邻下一级。
package promote

import (
	"fmt"

	"ontology/attest"
	"ontology/registry"
)

// Gate 是晋级闸门，组合仓库与证明存储。
type Gate struct {
	repo  *registry.Repo
	store *attest.Store
}

// New 创建晋级闸门。
func New(repo *registry.Repo, store *attest.Store) *Gate {
	return &Gate{repo: repo, store: store}
}

// Promote 把 from 级 (name, tag) 当前指向的摘要晋到紧邻下一级的同名标签。
// 拒绝次序：参数非法 > 时钟回退 > 无下一级 > 无权限 > 源标签缺失/已撤回 >
// 驻留不足 > 证明缺失 > 别名冲突 > 标签已撤回 > 标签不可变。
func (g *Gate) Promote(now int64, name, tag, from string, c registry.Caller) error {
	if now < 0 || now > registry.MaxNow || name == "" || tag == "" {
		return fmt.Errorf("%w: Promote(now=%d, name=%q, tag=%q)", registry.ErrInvalidArgument, now, name, tag)
	}
	g.repo.Lock()
	defer g.repo.Unlock()
	fromIdx := g.repo.StageIndexLocked(from)
	if fromIdx < 0 {
		return fmt.Errorf("%w: unknown stage %q", registry.ErrInvalidArgument, from)
	}
	if err := g.repo.CheckClockLocked(now); err != nil {
		return err
	}
	if fromIdx == g.repo.NumStages()-1 {
		return fmt.Errorf("%w: %q is the last stage", registry.ErrNoNextStage, from)
	}
	to := g.repo.StageAtLocked(fromIdx + 1)
	if !c.Has(registry.Promote, to.Name) {
		return fmt.Errorf("%w: Promote on %q", registry.ErrPermissionDenied, to.Name)
	}
	digest, err := g.repo.SourceDigestLocked(from, name, tag)
	if err != nil {
		return err
	}
	first, _ := g.repo.FirstLocked(from, name, digest)
	if now-first < to.S {
		return fmt.Errorf("%w: %s/%s digest %s dwelled %d < %d", registry.ErrDwell, from, name, digest, now-first, to.S)
	}
	valid := g.store.ValidTypesLocked(now, digest, g.repo.TrustedAtLocked(fromIdx+1))
	for _, typ := range to.Required {
		if !valid[typ] {
			return fmt.Errorf("%w: %s", registry.ErrAttestation, typ)
		}
	}
	if err := g.repo.LandLocked(now, to.Name, name, tag, digest); err != nil {
		return err
	}
	g.repo.CommitClockLocked(now)
	return nil
}
