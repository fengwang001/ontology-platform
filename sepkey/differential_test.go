package sepkey

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// randKey 生成随机键：以一定概率复用已有键的前缀制造公共前缀，
// 以一定概率产生 0xFF 字节，覆盖缩短与不缩短分支。
func randKey(r *rand.Rand, pool [][]byte) []byte {
	var k []byte
	if len(pool) > 0 && r.Intn(2) == 0 {
		base := pool[r.Intn(len(pool))]
		k = append(k, base[:r.Intn(len(base)+1)]...)
	}
	n := 1 + r.Intn(6)
	for len(k) < n {
		b := byte(r.Intn(256))
		if r.Intn(8) == 0 {
			b = 0xFF
		}
		k = append(k, b)
	}
	if len(k) == 0 {
		k = []byte{0}
	}
	return k
}

func formatKeys(keys [][]byte) string {
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(" ")
		}
		fmt.Fprintf(&sb, "%q", k)
	}
	return sb.String()
}

// 与朴素实现对拍 2000 组随机键序列：
// 逐字节比对分隔键、校验不变式、对拍 Seek，并打印输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	r := rand.New(rand.NewSource(20261001))
	for round := 0; round < 2000; round++ {
		n := 1 + r.Intn(5) // 块数
		need := 2*n - 1    // 排序去重后依次取 last/next 交替
		seen := map[string]struct{}{}
		var keys [][]byte
		for len(keys) < need {
			k := randKey(r, keys)
			if _, ok := seen[string(k)]; ok {
				continue
			}
			seen[string(k)] = struct{}{}
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 })

		var b Builder
		var wantSeps [][]byte
		var reasons []string
		var lasts, nexts [][]byte
		for i := 0; i < n-1; i++ {
			last, next := keys[2*i], keys[2*i+1]
			lasts = append(lasts, last)
			nexts = append(nexts, next)
			if err := b.AddBlock(last, next); err != nil {
				t.Fatalf("第 %d 组 AddBlock(%q, %q) 被意外拒绝: %v", round, last, next, err)
			}
			sep, why := naiveSep(last, next)
			wantSeps = append(wantSeps, sep)
			reasons = append(reasons, fmt.Sprintf("块%d last=%q next=%q: %s", i, last, next, why))
		}
		finLast := keys[2*n-2]
		lasts = append(lasts, finLast)
		if err := b.Finish(finLast); err != nil {
			t.Fatalf("第 %d 组 Finish(%q) 被意外拒绝: %v", round, finLast, err)
		}
		finSep, finWhy := naiveFinishSep(finLast)
		wantSeps = append(wantSeps, finSep)
		reasons = append(reasons, fmt.Sprintf("最后块 last=%q: %s", finLast, finWhy))

		got := b.Seps()
		if len(got) != len(wantSeps) {
			t.Fatalf("第 %d 组分隔键数量 %d, 期望 %d", round, len(got), len(wantSeps))
		}
		for i := range wantSeps {
			if !bytes.Equal(got[i], wantSeps[i]) {
				t.Fatalf("第 %d 组 seps[%d]=%q, 朴素实现为 %q\n判定依据: %s", round, i, got[i], wantSeps[i], reasons[i])
			}
			// 不变式: last <= sep
			if bytes.Compare(got[i], lasts[i]) < 0 {
				t.Fatalf("第 %d 组违反 last<=sep: last=%q sep=%q", round, lasts[i], got[i])
			}
			// 不变式: 非最后块 sep < next
			if i < len(nexts) && bytes.Compare(got[i], nexts[i]) >= 0 {
				t.Fatalf("第 %d 组违反 sep<next: sep=%q next=%q", round, got[i], nexts[i])
			}
		}

		// Seek 对拍: 块内键（last_i 与 next_i）必须命中真实所在块，
		// 随机探针与朴素线性扫描结果一致。
		var probes [][]byte
		for i := 0; i < n; i++ {
			probes = append(probes, lasts[i])
			if i < n-1 {
				probes = append(probes, nexts[i])
			}
		}
		for j := 0; j < 4; j++ {
			probes = append(probes, randKey(r, keys))
		}
		var seekLog []string
		for _, key := range probes {
			idx, err := b.Seek(key)
			want := naiveSeek(wantSeps, key)
			if want == -1 {
				if err == nil || idx != -1 {
					t.Fatalf("第 %d 组 Seek(%q)=(%d,%v), 期望越界", round, key, idx, err)
				}
			} else {
				if err != nil || idx != want {
					t.Fatalf("第 %d 组 Seek(%q)=(%d,%v), 朴素实现为块 %d", round, key, idx, err, want)
				}
			}
			seekLog = append(seekLog, fmt.Sprintf("Seek(%q)=%d", key, want))
		}
		// 块内键落块校验: Seek(last_i)==i; Seek(next_i)==i+1
		for i := 0; i < n; i++ {
			if idx, _ := b.Seek(lasts[i]); idx != i {
				t.Fatalf("第 %d 组 Seek(last[%d]=%q)=%d, 期望 %d", round, i, lasts[i], idx, i)
			}
			if i < n-1 {
				if idx, _ := b.Seek(nexts[i]); idx != i+1 {
					t.Fatalf("第 %d 组 Seek(next[%d]=%q)=%d, 期望 %d", round, i, nexts[i], idx, i+1)
				}
			}
		}

		t.Logf("第 %d 组 输入键池: %s", round, formatKeys(keys))
		t.Logf("第 %d 组 输出分隔键: %s", round, formatKeys(got))
		t.Logf("第 %d 组 判定依据: %s", round, strings.Join(reasons, " | "))
		t.Logf("第 %d 组 Seek 对拍: %s", round, strings.Join(seekLog, " "))
	}
}
