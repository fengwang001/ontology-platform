package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// participant 是通过隔离检查、可参与内容裁决的副本。
type participant struct {
	snap   Snapshot
	digest string
}

// quarantineResult 是损坏隔离阶段的输出。
type quarantineResult struct {
	participants []participant
	faults       []ReplicaFault
	// hints 记录仅存在于被隔离副本中的对象 ID（存在性线索），
	// 用于判定“剩余副本不足以确定取值”的不可和解情形。
	hints map[string][]string
}

// isolate 逐个解析副本信封，将结构性损坏（整体或局部）的副本
// 隔离出内容裁决。任何损坏都导致整个副本被隔离；局部可读的
// 被隔离副本仍贡献对象存在性线索。输出按（副本 ID, 摘要）排序，
// 与输入到达顺序无关。
func isolate(blobs [][]byte) quarantineResult {
	res := quarantineResult{hints: map[string][]string{}}
	for _, blob := range blobs {
		sum := sha256.Sum256(blob)
		digest := hex.EncodeToString(sum[:])
		d := decodeEnvelope(blob)

		switch {
		case !d.headerOK:
			res.faults = append(res.faults, ReplicaFault{
				Kind:   CorruptWhole,
				Detail: "头部不可读或校验失败，整个副本无法解析",
				Digest: digest,
			})
		case len(d.badEntries) > 0:
			res.faults = append(res.faults, ReplicaFault{
				ReplicaID: d.snap.ReplicaID,
				Kind:      CorruptPartial,
				Detail:    fmt.Sprintf("条目校验失败: %v", d.badEntries),
				Digest:    digest,
			})
			for _, obj := range d.snap.Objects {
				res.hints[obj.ObjectID] = append(res.hints[obj.ObjectID], d.snap.ReplicaID)
			}
		default:
			res.participants = append(res.participants, participant{snap: d.snap, digest: digest})
		}
	}

	sort.Slice(res.participants, func(i, j int) bool {
		a, b := res.participants[i], res.participants[j]
		if a.snap.ReplicaID != b.snap.ReplicaID {
			return a.snap.ReplicaID < b.snap.ReplicaID
		}
		return a.digest < b.digest
	})
	sort.Slice(res.faults, func(i, j int) bool {
		a, b := res.faults[i], res.faults[j]
		if a.ReplicaID != b.ReplicaID {
			return a.ReplicaID < b.ReplicaID
		}
		return a.Digest < b.Digest
	})
	for id := range res.hints {
		sort.Strings(res.hints[id])
	}
	return res
}
