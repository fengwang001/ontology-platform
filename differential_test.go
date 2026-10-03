package bucket_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	bucket "ontology"
	"ontology/authz"
)

// pickKV 返回 (key, ver)。80% 选真实出现过的同键版本，20% 版本号
// 故意偏移或换键以触发 notexist；两侧对 (key,ver) 不一致的判定必须相同。
func pickKV(rng *rand.Rand, known []struct {
	k string
	v int64
}, keys []string) (string, int64) {
	if len(known) == 0 || rng.Intn(5) == 0 {
		return keys[rng.Intn(len(keys)-1)], int64(1 + rng.Intn(9))
	}
	kv := known[rng.Intn(len(known))]
	if rng.Intn(5) == 0 {
		kv.v += int64(1 + rng.Intn(5))
	}
	return kv.k, kv.v
}

// TestRandomAgainstNaive 1500 组随机操作序列逐步对照；日志打印每步
// 输入、两侧输出与判定依据，仅在失败时随 fatalf 一并呈现。
func TestRandomAgainstNaive(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 1500 组随机对照")
	}
	const seqs = 1500
	for seed := int64(0); seed < seqs; seed++ {
		rng := rand.New(rand.NewSource(seed))
		mode, nmode := bucket.GOVERNANCE, nGov
		if rng.Intn(2) == 0 {
			mode, nmode = bucket.COMPLIANCE, nComp
		}
		d := int64(0)
		switch rng.Intn(3) {
		case 1:
			d = rng.Int63n(101)
		case 2:
			d = rng.Int63n(1_000_000_001)
		}
		b := bucket.New(mode, d)
		n := newNaive(nmode, d)
		keys := []string{"a", "b", "c", ""}
		type kv = struct {
			k string
			v int64
		}
		var known []kv
		var log []string

		failf := func(msg string, args ...any) {
			t.Fatalf("seed=%d\n%s\n%s", seed, strings.Join(log, "\n"), fmt.Sprintf(msg, args...))
		}
		cmp := func(step int, in string, bv, nv int64, bc, nc string) {
			log = append(log, fmt.Sprintf("step %d | 输入: %s | 真实: v=%d %s | 模拟: v=%d %s", step, in, bv, bc, nv, nc))
			if bc != nc || bv != nv {
				failf("分歧 at step %d", step)
			}
		}
		cmpAudit := func(step int) {
			ba := b.Audit()
			if len(ba) != len(n.aud) {
				failf("step %d 审计长度 %d vs %d\n%v\n%v", step, len(ba), len(n.aud), ba, n.aud)
			}
			for i := range ba {
				if string(ba[i].Key) != n.aud[i].key || ba[i].Ver != n.aud[i].ver ||
					ba[i].Now != n.aud[i].now || ba[i].OldUntil != n.aud[i].oldUntil {
					failf("step %d 审计项 %d 不一致: %+v vs %+v", step, i, ba[i], n.aud[i])
				}
			}
		}

		steps := 25 + rng.Intn(35)
		for step := 0; step < steps; step++ {
			var perms []authz.Permission
			if rng.Intn(2) == 0 {
				perms = append(perms, pDel)
			}
			if rng.Intn(2) == 0 {
				perms = append(perms, pByp)
			}
			if rng.Intn(2) == 0 {
				perms = append(perms, pPut)
			}
			opr := op(perms...)
			hd := hasPerm(opr, pDel)
			hb := hasPerm(opr, pByp)
			hp := hasPerm(opr, pPut)

			now := n.now + int64(rng.Intn(130)) - 6
			if rng.Intn(25) == 0 {
				now = -1 - rng.Int63n(3)
			}
			key := keys[rng.Intn(len(keys))]
			bypass := rng.Intn(2) == 0

			switch rng.Intn(8) {
			case 0:
				size := rng.Int63n(130)
				if rng.Intn(15) == 0 {
					size = -1
				}
				bv, be := b.Put(opr, k(key), size, now)
				nv, nc := n.put(key, size, now)
				cmp(step, fmt.Sprintf("Put(%q,%d,%d) perms=%v", key, size, now, perms), bv, nv, errCode(be), nc)
				if nc == "ok" {
					known = append(known, kv{key, nv})
				}
			case 1:
				bv, be := b.Delete(opr, k(key), now)
				nv, nc := n.del(key, now)
				cmp(step, fmt.Sprintf("Delete(%q,%d)", key, now), bv, nv, errCode(be), nc)
				if nc == "ok" {
					known = append(known, kv{key, nv})
				}
			case 2:
				ov, be := b.Get(k(key))
				nv, nc := n.get(key)
				bv := int64(0)
				if be == nil {
					bv = ov.Ver
				}
				cmp(step, fmt.Sprintf("Get(%q)", key), bv, nv, errCode(be), nc)
			case 3:
				vkey, ver := pickKV(rng, known, keys)
				key = vkey
				be := b.DeleteVersion(opr, k(key), ver, bypass, now)
				nc := n.delVersion(hd, hb, key, ver, bypass, now)
				cmp(step, fmt.Sprintf("DeleteVersion(%q,v%d,bypass=%v,%d) perms=%v", key, ver, bypass, now, perms), ver, ver, errCode(be), nc)
			case 4, 5:
				vkey, ver := pickKV(rng, known, keys)
				key = vkey
				var mm bucket.Mode
				var nm2 int
				switch rng.Intn(3) {
				case 0:
					mm, nm2 = bucket.Mode(nNone), nNone
				case 1:
					mm, nm2 = bucket.Mode(nGov), nGov
				default:
					mm, nm2 = bucket.Mode(nComp), nComp
				}
				until := now + 1 + rng.Int63n(160)
				if rng.Intn(8) == 0 {
					until = now // 触发 until<=now 参数非法
				}
				be := b.SetRetention(opr, k(key), ver, mm, until, bypass, now)
				nc := n.setRetention(hp, hb, key, ver, nm2, until, bypass, now)
				cmp(step, fmt.Sprintf("SetRetention(%q,v%d,m=%d,until=%d,b=%v,%d) perms=%v", key, ver, nm2, until, bypass, now, perms), ver, ver, errCode(be), nc)
			case 6:
				vkey, ver := pickKV(rng, known, keys)
				key = vkey
				on := rng.Intn(2) == 0
				be := b.SetHold(opr, k(key), ver, on, now)
				nc := n.setHold(hp, key, ver, on, now)
				cmp(step, fmt.Sprintf("SetHold(%q,v%d,on=%v,%d) perms=%v", key, ver, on, now, perms), ver, ver, errCode(be), nc)
			case 7:
				m := 1 + rng.Intn(4)
				items := make([]bucket.Item, m)
				nitems := make([]nItem, m)
				for i := 0; i < m; i++ {
					kk, vv := pickKV(rng, known, keys)
					if kk == "" {
						kk = "a"
					}
					items[i] = bucket.Item{Key: k(kk), Ver: vv}
					nitems[i] = nItem{key: kk, ver: vv}
				}
				if rng.Intn(6) == 0 && len(items) > 1 {
					items[1] = items[0]
					nitems[1] = nitems[0]
				}
				be := b.DeleteVersions(opr, items, bypass, now)
				nc := n.delVersions(hd, hb, nitems, bypass, now)
				cmp(step, fmt.Sprintf("DeleteVersions(%v,b=%v,%d) perms=%v", nitems, bypass, now, perms), 0, 0, batchCode(be), nc)
			}
			if rng.Intn(4) == 0 {
				cmpAudit(step)
			}
		}
		cmpAudit(steps)
		t.Logf("seed=%d 完成（%d 步，D=%d, mode=%d）", seed, steps, d, nmode)
	}
}

func hasPerm(o authz.Operator, p authz.Permission) bool { return o != nil && o.Has(p) }
