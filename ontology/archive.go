package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Effect 是规则判定结果。
type Effect string

const (
	EffectAllow Effect = "ALLOW"
	EffectDeny  Effect = "DENY"
)

// RuleVersion 是某条规则在某一时刻命中时的不可变内容快照。
// 该对象一经写入规则档案即不再修改、不再删除。
type RuleVersion struct {
	VersionID      string
	RuleID         string
	DeclaringType  string
	Subject        string
	Attribute      string
	Effect         Effect
	ContentHash    string
	SealedAtSeq    int64
	SealedAtMillis int64
}

// ruleArchive 是只增不改的规则版本档案，VersionID 是内容寻址标识。
type ruleArchive struct {
	versions map[string]*RuleVersion
	// reads 仅用于可验证的 O(1) 性能断言：成功解析一次固化依据的 map 访问次数。
	reads int
}

func newRuleArchive() *ruleArchive { return &ruleArchive{versions: map[string]*RuleVersion{}} }

// canonicalHash 以规则内容字段（不含时间戳/序号）计算内容指纹。
// VersionID 内容寻址：同内容必同标识，含义永远不会漂移到另一条规则。
func canonicalHash(ruleID, declaringType, subject, attribute string, effect Effect) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s", ruleID, declaringType, subject, attribute, effect)
	return hex.EncodeToString(h.Sum(nil))
}

// ruleIdentity 生成一条规则的稳定标识：由规则所在的“判定位置”唯一确定。
// 命中点 = 声明类型 + 执行主体 + 属性。
func ruleIdentity(declaringType, subject, attribute string) string {
	h := sha256.New()
	fmt.Fprintf(h, "rule\x00%s\x00%s\x00%s", declaringType, subject, attribute)
	return "r_" + hex.EncodeToString(h.Sum(nil))[:16]
}

// seal 把某条规则在当前时刻的内容固化为不可变版本并写入档案。
// 相同内容重复声明时复用既有 VersionID（幂等），但不影响历史导出已持有的标识。
func (a *ruleArchive) seal(ruleID, declaringType, subject, attribute string, effect Effect, seq int64) *RuleVersion {
	hash := canonicalHash(ruleID, declaringType, subject, attribute, effect)
	versionID := "rv_" + hash[:24]
	if v, ok := a.versions[versionID]; ok {
		return v
	}
	v := &RuleVersion{
		VersionID:      versionID,
		RuleID:         ruleID,
		DeclaringType:  declaringType,
		Subject:        subject,
		Attribute:      attribute,
		Effect:         effect,
		ContentHash:    hash,
		SealedAtSeq:    seq,
		SealedAtMillis: time.Now().UnixMilli(),
	}
	a.versions[versionID] = v
	return v
}

// resolve 依据不可变 VersionID 取回命中时刻的规则内容快照。
// 复杂度为单次哈希表访问：O(1)，与该类型体系历史规则变更总次数无关。
func (a *ruleArchive) resolve(versionID string) (*RuleVersion, bool) {
	a.reads++
	v, ok := a.versions[versionID]
	if !ok {
		return nil, false
	}
	// 返回副本，杜绝调用方修改档案。
	cp := *v
	return &cp, true
}

// archiveReads 返回自档案创建以来成功/尝试解析累计的访问计数（测试可验证用）。
func (a *ruleArchive) archiveReads() int { return a.reads }
