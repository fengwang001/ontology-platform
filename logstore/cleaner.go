package logstore

import (
	"fmt"
	"math/big"
	"sort"
)

// score 表示一个已封存段的代价收益评分，使用精确有理数比较。
type score struct {
	num *big.Int
	den *big.Int
}

// pickVictim 按代价收益挑选已封存段。调用方持有写锁。
//
// 存活率 r = live / segmentSize，年龄 age = clock - seg.maxTS，
// 评分 = (1-r) * age / (1+r)，取最大；并列取段号最小。
// 存活率为零的段直接回收（同样按段号最小挑）。
func (s *Store) pickVictim() (*segment, string) {
	sealed := make([]*segment, 0, len(s.segments))
	for id, seg := range s.segments {
		if id == s.activeID {
			continue
		}
		if seg.sealed {
			sealed = append(sealed, seg)
		}
	}
	if len(sealed) == 0 {
		return nil, ""
	}
	sort.Slice(sealed, func(i, j int) bool { return sealed[i].id < sealed[j].id })

	var zeroVictim *segment
	for _, seg := range sealed {
		if s.live[seg.id] == 0 {
			if zeroVictim == nil || seg.id < zeroVictim.id {
				zeroVictim = seg
			}
		}
	}
	if zeroVictim != nil {
		age := s.clock - zeroVictim.maxTS
		basis := fmt.Sprintf("zero-live seg=%d age=%d", zeroVictim.id, age)
		s.logf("decision pickVictim %s", basis)
		return zeroVictim, basis
	}

	var best *segment
	var bestScore score
	S := new(big.Int).SetUint64(s.segmentSize)
	for _, seg := range sealed {
		live := new(big.Int).SetUint64(s.live[seg.id])
		age := new(big.Int).SetUint64(s.clock - seg.maxTS)

		// (1-r) = (S-live)/S, (1+r) = (S+live)/S
		// score = (S-live)*age / (S+live)；S 因子在两侧消去。
		num := new(big.Int).Mul(new(big.Int).Sub(S, live), age)
		den := new(big.Int).Add(S, live)
		sc := score{num: num, den: den}
		s.logf("decision candidate seg=%d live=%d age=%s score=%s/%s",
			seg.id, s.live[seg.id], age.String(), num.String(), den.String())
		if best == nil || compareScore(sc, bestScore) > 0 {
			best = seg
			bestScore = sc
		}
	}
	basis := fmt.Sprintf("score seg=%d value=%s/%s", best.id, bestScore.num.String(), bestScore.den.String())
	s.logf("decision pickVictim %s", basis)
	return best, basis
}

// compareScore 精确比较 a 与 b：a>b 返回 1，相等返回 0，a<b 返回 -1。
// 分母恒正，交叉相乘不会引入符号歧义。
func compareScore(a, b score) int {
	left := new(big.Int).Mul(a.num, b.den)
	right := new(big.Int).Mul(b.num, a.den)
	return left.Cmp(right)
}

// migrateAndReclaim 把 victim 中存活的块按原写入顺序搬到日志尾，
// 保留原写入时刻；搬迁完成后整段回收。调用方持有写锁。
func (s *Store) migrateAndReclaim(victim *segment, basis string, allowNewSegment bool) error {
	s.logf("decision migrate start victim=%d live=%d used=%d basis=%s",
		victim.id, s.live[victim.id], victim.used, basis)

	oldID := victim.id
	blocks := victim.blocks

	// 先释放受害段：段槽位与段号立即可复用，保证段总数不超过上限。
	delete(s.segments, oldID)
	delete(s.live, oldID)
	s.freeIDs = append(s.freeIDs, oldID)
	sort.Ints(s.freeIDs)

	// 按原写入顺序搬迁（段内块严格按 ts 递增追加）。
	var migrated, dropped uint64
	for _, b := range blocks {
		size := blockSize(b.key, b.value, b.kind)
		alive, reason := s.blockAliveAfterClean(oldID, b, size)

		locs := s.index[b.key]
		at := -1
		for i := range locs {
			if locs[i].ts == b.ts {
				at = i
				break
			}
		}
		if !alive {
			if at >= 0 {
				s.index[b.key] = append(locs[:at], locs[at+1:]...)
				if len(s.index[b.key]) == 0 {
					delete(s.index, b.key)
				}
			}
			dropped += size
			s.logf("decision drop during-migration seg=%d key=%q ts=%d kind=%d reason=%s",
				oldID, b.key, b.ts, b.kind, reason)
			continue
		}

		needRoll := s.activeID < 0 || s.segments[s.activeID].free(s.segmentSize) < size
		if needRoll && !allowNewSegment {
			// 调用方已用 live <= activeFree 预判；这是防御性拒绝。
			return ErrSpaceExhausted
		}
		if needRoll {
			s.rollActive()
		}
		dst := s.segments[s.activeID]
		offset := dst.append(b, size)
		if at >= 0 {
			locs[at] = loc{segID: dst.id, offset: offset, size: size, ts: b.ts, kind: b.kind}
			s.index[b.key] = locs
		}
		migrated += size
		s.logf("decision migrate key=%q ts=%d seg=%d->%d size=%d kind=%d reason=%s (ts preserved)",
			b.key, b.ts, oldID, dst.id, size, b.kind, reason)
	}

	s.recomputeLive()
	s.logf("decision reclaim seg=%d migrated=%d dropped=%d freeSegments=%d",
		oldID, migrated, dropped, s.freeSegmentCount())
	return nil
}

// blockAliveAfterClean 判定 victim 段内某块在本次回收后是否仍需存活：
//
//   - 值块：当且仅当它仍是该键索引中的最新块（最新块是值块）。
//   - 墓碑块：当且仅当任一其他段中仍留有该键更旧的块。
//     本次搬迁的目标段与 victim 是不同段，因此墓碑存活规则在搬迁后
//     仍按“其他段”重新判定，随旧块一起搬运或一同消亡。
func (s *Store) blockAliveAfterClean(victimID int, b *block, size uint64) (bool, string) {
	locs := s.index[b.key]
	if b.kind == kindValue {
		if len(locs) > 0 {
			last := locs[len(locs)-1]
			if last.kind == kindValue && last.ts == b.ts {
				return true, "latest-value"
			}
		}
		return false, "superseded"
	}

	// 墓碑：victim 已从段集合中删除，本判定基于当前索引快照：
	// 若索引中任一其他段仍留有该键更旧的块则保留。
	tombSeg := victimID
	for i := len(locs) - 1; i >= 0; i-- {
		if locs[i].ts == b.ts && locs[i].kind == kindTomb {
			tombSeg = locs[i].segID
			break
		}
	}
	for _, l := range locs {
		if l.ts < b.ts && l.segID != tombSeg {
			return true, "older-block-in-other-segment"
		}
	}
	return false, "no-older-block-elsewhere"
}

// recomputeLive 依据索引逐段重算存活字节，作为搬迁后的权威账目。
// 存活字节 = 索引指向该段的最新值块字节 + 该段存活墓碑字节。
func (s *Store) recomputeLive() {
	live := make(map[int]uint64, len(s.segments))
	for _, locs := range s.index {
		for i, l := range locs {
			if l.kind == kindValue {
				// 仅该键的最新块（且为值块）存活。
				if i == len(locs)-1 {
					live[l.segID] += l.size
				}
			} else {
				alive := false
				for _, older := range locs {
					if older.ts < l.ts && older.segID != l.segID {
						alive = true
						break
					}
				}
				if alive {
					live[l.segID] += l.size
				}
				reason := "older-block-in-other-segment"
				if !alive {
					reason = "no-older-block-elsewhere"
				}
				s.logf("decision tombstone-account ts=%d seg=%d alive=%t reason=%s",
					l.ts, l.segID, alive, reason)
			}
		}
	}
	s.live = live
}
