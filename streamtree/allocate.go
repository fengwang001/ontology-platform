package streamtree

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

func (a *Allocator) Allocate(quota int, ready ...int) (map[int]int, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	readySet := make(map[int]struct{}, len(ready))
	for _, id := range ready {
		if a.streams[id] == nil {
			reason := fmt.Sprintf("rejected: %s stream=%d; quota=%d", ErrReadyStreamNotFound.Error(), id, quota)
			a.logAllocate(quota, ready, nil, reason)
			return nil, ErrReadyStreamNotFound
		}
		readySet[id] = struct{}{}
	}

	if quota < 0 {
		reason := fmt.Sprintf("rejected: %s; ready=%v", ErrNegativeQuota.Error(), ready)
		a.logAllocate(quota, ready, nil, reason)
		return nil, ErrNegativeQuota
	}

	shares := map[int]int{}
	a.allocateNode(RootID, quota, readySet, shares)

	reason := "accepted: quota split over ready-containing siblings; base=floor(quota*weight/sum_weights); one extra byte goes to the largest quota*weight mod sum_weights remainders, ties use smaller stream id; ready parent consumes its whole subtree share"
	a.logAllocate(quota, ready, shares, reason)
	return shares, nil
}

func (a *Allocator) allocateNode(id, quota int, ready map[int]struct{}, shares map[int]int) {
	if id != RootID {
		if _, ok := ready[id]; ok {
			shares[id] += quota
			return
		}
	}

	eligible := make([]int, 0, len(a.children[id]))
	for child := range a.children[id] {
		if a.subtreeHasReady(child, ready) {
			eligible = append(eligible, child)
		}
	}
	sort.Ints(eligible)
	a.splitQuota(id, quota, eligible, ready, shares)
}

func (a *Allocator) splitQuota(parent, quota int, eligible []int, ready map[int]struct{}, shares map[int]int) {
	totalWeight := 0
	for _, id := range eligible {
		totalWeight += a.streams[id].weight
	}
	if totalWeight == 0 || quota == 0 || len(eligible) == 0 {
		return
	}

	type part struct {
		id        int
		base      int
		remainder *big.Int
	}

	bigQuota := big.NewInt(int64(quota))
	bigTotal := big.NewInt(int64(totalWeight))
	parts := make([]part, 0, len(eligible))
	used := 0

	for _, id := range eligible {
		numerator := new(big.Int).Mul(bigQuota, big.NewInt(int64(a.streams[id].weight)))
		base, remainder := new(big.Int).QuoRem(numerator, bigTotal, new(big.Int))
		baseQuota := int(base.Int64())
		used += baseQuota
		parts = append(parts, part{id: id, base: baseQuota, remainder: remainder})
	}

	extra := quota - used
	sort.SliceStable(parts, func(i, j int) bool {
		cmp := parts[i].remainder.Cmp(parts[j].remainder)
		if cmp != 0 {
			return cmp > 0
		}
		return parts[i].id < parts[j].id
	})

	if extra > len(parts) {
		extra = len(parts)
	}
	for index := range parts {
		if index < extra {
			parts[index].base++
		}
		a.allocateNode(parts[index].id, parts[index].base, ready, shares)
	}
}

func (a *Allocator) subtreeHasReady(id int, ready map[int]struct{}) bool {
	if _, ok := ready[id]; ok {
		return true
	}
	for child := range a.children[id] {
		if a.subtreeHasReady(child, ready) {
			return true
		}
	}
	return false
}

func (a *Allocator) logAllocate(quota int, ready []int, shares map[int]int, reason string) {
	if a.w == nil {
		return
	}

	a.logMu.Lock()
	defer a.logMu.Unlock()

	fmt.Fprintf(a.w, "op=allocate input={quota:%d ready:[%s]} output=%s basis=%q\n",
		quota, joinInts(ready), formatShareMap(shares), reason)
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Itoa(value)
	}
	return strings.Join(parts, ",")
}

func formatShareMap(shares map[int]int) string {
	if shares == nil {
		return "map[]"
	}

	ids := make([]int, 0, len(shares))
	for id := range shares {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var builder strings.Builder
	builder.WriteString("map[")
	for index, id := range ids {
		if index > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(strconv.Itoa(id))
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(shares[id]))
	}
	builder.WriteByte(']')
	return builder.String()
}
