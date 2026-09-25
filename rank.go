package ontology

// Rank computes ROW_NUMBER / RANK / DENSE_RANK for every accepted row,
// independently per partition.
//
// Rows with a nil Partition or a NaN Value are rejected and counted in the
// returned Result; they take no part in ranking. The input slice and all Row
// values are left unchanged; Rankings is a freshly allocated slice ordered by
// (partition lexicographic, value direction, ID ascending).
func Rank(rows []Row, dir Direction) Result {
	groups := map[string][]entry{}
	var skippedNil, skippedNaN int

	for _, r := range rows {
		if r.Partition == nil {
			skippedNil++
			continue
		}
		if isNaN(r.Value) {
			skippedNaN++
			continue
		}
		key := *r.Partition
		groups[key] = append(groups[key], entry{
			partition: key,
			value:     r.Value,
			id:        r.ID,
		})
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sortStrings(keys)

	out := make([]Ranking, 0, len(rows)-skippedNil-skippedNaN)
	var comparisons int64
	cmp := makeComparator(dir)
	for _, key := range keys {
		g := groups[key]
		comparisons += mergeSort(g, cmp)
		appendRankings(&out, g)
	}

	return Result{
		Rankings:        out,
		SkippedNilPart:  skippedNil,
		SkippedNaN:      skippedNaN,
		ComparisonCount: comparisons,
	}
}

func makeComparator(dir Direction) comparator {
	return func(a, b entry) int {
		c := compareValues(a.value, b.value)
		if dir == Desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return compareStrings(a.id, b.id)
	}
}

// appendRankings appends the three rank columns for one already-sorted
// partition. Equal values form a tie group: ROW_NUMBER walks position,
// RANK jumps to the position after the tie group, DENSE_RANK advances by one.
func appendRankings(out *[]Ranking, g []entry) {
	pos := 0
	for i := 0; i < len(g); {
		j := i + 1
		for j < len(g) && compareValues(g[j].value, g[i].value) == 0 {
			j++
		}
		rank := i + 1
		dense := denseRankOf(g[:j])
		for k := i; k < j; k++ {
			pos++
			*out = append(*out, Ranking{
				Partition: g[k].partition,
				ID:        g[k].id,
				Value:     g[k].value,
				RowNumber: pos,
				Rank:      rank,
				DenseRank: dense,
			})
		}
		i = j
	}
}

// denseRankOf reports the dense rank of the last tie group in prefix g,
// i.e. one plus the number of distinct strictly-earlier values.
func denseRankOf(g []entry) int {
	dense := 1
	for i := 1; i < len(g); i++ {
		if compareValues(g[i].value, g[i-1].value) != 0 {
			dense++
		}
	}
	return dense
}
