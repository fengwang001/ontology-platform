package instance

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveVersion 独立保留每一条已接受写入的全部字段，绝不做任何物理丢弃。
type naiveVersion struct {
	sys, biz, base int64
	deleted        bool
	payload        string
}

// naiveOracle 是题目要求的“独立维护全部版本的朴素线性扫描模型”。
// 它不使用 treap、不做任何索引，查询时全量扫描该主键的全部版本。
type naiveOracle struct {
	versions []naiveVersion
}

// accept 按与 Store 完全相同的规则判定写入是否被接受；
// 系统版本号先按链内序号占位，接受后由测试用真实全局版本回填。
func (o *naiveOracle) accept(biz, base int64, deleted bool) error {
	var latest int64 // 该键最近一次接受记录的全局系统版本（0=尚未写入）
	earliest := int64(-1)
	if len(o.versions) > 0 {
		latest = o.versions[len(o.versions)-1].sys
		earliest = o.versions[0].biz
	}
	if base != latest {
		return ErrConflict
	}
	if earliest >= 0 && biz < earliest {
		return ErrBeforeEarliestBiz
	}
	o.versions = append(o.versions, naiveVersion{
		sys: 0, biz: biz, base: base, deleted: deleted, // sys 由测试回填
	})
	return nil
}

// scan 线性扫描全部版本：筛 sys<=S，按 biz 起点分组取 sys 最大者，
// 再取覆盖 bizAt 的最大起点。O(版本总数)。
func (o *naiveOracle) scan(S, bizAt int64) (kind StateKind, ver int64) {
	latestPerStart := map[int64]naiveVersion{}
	for _, v := range o.versions {
		// 先按系统时间过滤（sys 是全局编号），再对同一起点取系统时间最大者。
		if v.sys > S {
			continue
		}
		if cur, ok := latestPerStart[v.biz]; !ok || v.sys > cur.sys {
			latestPerStart[v.biz] = v
		}
	}
	var bestBiz int64
	var bestVer naiveVersion
	found := false
	// 收集起点后排序，模拟“取 <= bizAt 的最大起点”。
	starts := make([]int64, 0, len(latestPerStart))
	for b := range latestPerStart {
		starts = append(starts, b)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	for _, b := range starts {
		if b <= bizAt {
			bestBiz = b
			bestVer = latestPerStart[b]
			found = true
		}
	}
	if !found {
		return StateUnknown, 0
	}
	_ = bestBiz
	if bestVer.deleted {
		return StateAbsent, bestVer.sys
	}
	return StatePresent, bestVer.sys
}

// TestRandomDifferential 在大规模随机写入/删除/查询序列下逐条对照
// 真实 Store 与朴素线性扫描模型；每次操作打印输入、实际输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const (
		keys        = 8
		iterations  = 4000
		queryEvery  = 3
		bizDomain   = 40 // 小业务域制造大量同起点覆盖
		staleChance = 12 // 百分比：故意使用陈旧凭证制造冲突
		earlyChance = 6  // 百分比：故意早于最早边界
		delChance   = 30 // 百分比：写入是逻辑删除
	)
	rng := rand.New(rand.NewSource(20261008))

	s := New()
	oracles := map[string]*naiveOracle{}
	// 每个键最近一次接受记录的全局系统版本（乐观凭证的真实取值）；0=尚未写入。
	oracleSys := map[string]int64{}

	// 统计真实索引访问，用于复杂度断言（见 TestQueryComplexityGuards）。
	var maxVisits int64
	var totalVisits int64
	var queries int64

	for i := 0; i < iterations; i++ {
		pk := fmt.Sprintf("k%d", rng.Intn(keys))
		o := oracles[pk]
		if o == nil {
			o = &naiveOracle{}
			oracles[pk] = o
		}

		if i%queryEvery != 0 {
			// ---- 写入 / 删除 ----
			biz := int64(rng.Intn(bizDomain))
			deleted := rng.Intn(100) < delChance
			base := oracleSys[pk] // 正确凭证
			intent := "valid"
			stale := base > 0 && rng.Intn(100) < staleChance
			if stale {
				// 从该键历史版本里挑一个“格式合法但已过时”的凭证。
				if len(o.versions) >= 1 {
					idxPick := rng.Intn(len(o.versions))
					base = o.versions[idxPick].sys
				}
				if base == oracleSys[pk] {
					base = 0 // 极端情况下退化为“新建凭证冲突”
				}
				intent = "stale-token"
			}
			early := false
			if !stale && rng.Intn(100) < earlyChance && len(o.versions) > 0 && o.versions[0].biz > 0 {
				biz = rng.Int63n(o.versions[0].biz) // 保证 0 <= biz < earliest
				intent = "before-boundary"
				early = true
			}

			buildReq := func(b int64) WriteRequest {
				req := WriteRequest{
					ObjectType: "T", PrimaryKey: pk, BizStart: biz, Base: b,
					Deleted: deleted,
				}
				if !deleted {
					req.Payload = fmt.Sprintf("p@%d", i)
				}
				return req
			}
			req := buildReq(base)
			v, gotErr := s.Write(req)
			wantErr := o.accept(biz, base, deleted)

			tracef(t, "RAND[%d] WRITE pk=%s biz=%d base=%d del=%v intent=%s -> sys=%d err=%v | oracle err=%v",
				i, pk, biz, base, deleted, intent, v.SysVersion, gotErr, wantErr)

			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("acceptance mismatch at %d: store=%v oracle=%v req=%+v", i, gotErr, wantErr, req)
			}
			if gotErr != nil {
				if !sameErrorClass(gotErr, wantErr) {
					t.Fatalf("error class mismatch at %d: %v vs %v", i, gotErr, wantErr)
				}
				// 被拒绝写入不得消耗系统版本号。
				if v.SysVersion != 0 {
					t.Fatalf("rejected write returned nonzero sys=%d", v.SysVersion)
				}
				// 真实客户端语义：乐观冲突后带最新凭证重试一次，必须成功；
				// “早于边界”属于硬拒绝，重试仍应失败，不重试。
				bizLegal := biz >= o.versions[0].biz
				if stale && !early && bizLegal {
					fresh := oracleSys[pk]
					retry := buildReq(fresh)
					rv, rerr := s.Write(retry)
					rwant := o.accept(biz, fresh, deleted)
					tracef(t, "RAND[%d] RETRY pk=%s biz=%d base=%d -> sys=%d err=%v | oracle err=%v",
						i, pk, biz, fresh, rv.SysVersion, rerr, rwant)
					if rerr != nil || rwant != nil {
						t.Fatalf("retry with fresh token must succeed: %v %v", rerr, rwant)
					}
					o.versions[len(o.versions)-1].sys = rv.SysVersion
					oracleSys[pk] = rv.SysVersion
				}
				continue
			}
			// 接受：同步参照模型的系统版本号为真实全局版本。
			oracleSys[pk] = v.SysVersion
			o.versions[len(o.versions)-1].payload = v.Payload
			o.versions[len(o.versions)-1].sys = v.SysVersion
			continue
		}

		// ---- 双时态查询：S 覆盖 0..当前时钟，bizAt 覆盖边界内外 ----
		S := int64(0)
		if s.Clock() > 0 {
			S = rng.Int63n(s.Clock() + 2) // 允许超过最新（晚于全部记录）
		}
		bizAt := int64(rng.Intn(bizDomain + 10))

		// 真实路径：用访问计数版查询。
		c := s.chains[key{objType: "T", pk: pk}]
		var (
			gotKind StateKind
			gotVer  int64
			visits  int
		)
		if c == nil || (len(c.commits) > 0 && S < c.commits[0].SysVersion) {
			gotKind = StateUnknown
		} else if c != nil {
			if sv, ok2 := c.index.LastAsOf(S, bizAt, &visits); ok2 {
				gotVer = sv
				st := s.stateAtVersion(c, sv)
				gotKind = st.Kind
			} else {
				gotKind = StateUnknown
			}
		}
		wantKind, wantVer := o.scan(S, bizAt)
		tracef(t, "RAND[%d] QUERY pk=%s sys=%d biz=%d -> kind=%d ver=%d visits=%d | oracle kind=%d ver=%d (依据: 该键版本数=%d)",
			i, pk, S, bizAt, gotKind, gotVer, visits, wantKind, wantVer, len(o.versions))
		if gotKind != wantKind || gotVer != wantVer {
			t.Fatalf("query mismatch at %d pk=%s (S=%d,biz=%d): got(%d,v%d) want(%d,v%d)",
				i, pk, S, bizAt, gotKind, gotVer, wantKind, wantVer)
		}
		queries++
		totalVisits += int64(visits)
		if int64(visits) > maxVisits {
			maxVisits = int64(visits)
		}
	}

	// 复杂度护栏（经验验证，详细论证见 docs/DESIGN.md）：
	// 即便单键已累积数千版本，双坐标定位访问的节点数必须保持在对数级别。
	var maxChainLen int
	for _, o := range oracles {
		if len(o.versions) > maxChainLen {
			maxChainLen = len(o.versions)
		}
	}
	t.Logf("SUMMARY iterations=%d queries=%d maxChainLen=%d maxVisits=%d avgVisits=%.2f",
		iterations, queries, maxChainLen, maxVisits, float64(totalVisits)/float64(max(1, queries)))
	if maxVisits > 80 { // log2(数千) 量级的宽松上界，绝不允许线性
		t.Fatalf("query visits %d look linear w.r.t. chain length %d", maxVisits, maxChainLen)
	}
}

func sameErrorClass(a, b error) bool {
	switch {
	case errors.Is(a, ErrInvalidArgument) && errors.Is(b, ErrInvalidArgument):
		return true
	case errors.Is(a, ErrConflict) && errors.Is(b, ErrConflict):
		return true
	case errors.Is(a, ErrBeforeEarliestBiz) && errors.Is(b, ErrBeforeEarliestBiz):
		return true
	}
	return false
}
