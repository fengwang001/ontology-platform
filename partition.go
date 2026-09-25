package ontology

import (
	"math"
	"sort"
)

type workRow struct {
	partitionKey string
	sortValue    float64
	rowID        string
}

type partitionRows struct {
	key  string
	rows []workRow
}

func partitionInput(rows []InputRow) ([]partitionRows, SkipCounts) {
	indexByKey := make(map[string]int)
	partitions := make([]partitionRows, 0)
	skipped := SkipCounts{}

	for _, row := range rows {
		missingKey := row.PartitionKey == nil
		nanValue := math.IsNaN(row.SortValue)
		if missingKey {
			skipped.MissingPartitionKey++
		}
		if nanValue {
			skipped.NaNSortValue++
		}
		if missingKey || nanValue {
			skipped.Total++
			continue
		}

		key := *row.PartitionKey
		index, ok := indexByKey[key]
		if !ok {
			index = len(partitions)
			indexByKey[key] = index
			partitions = append(partitions, partitionRows{key: key})
		}
		partitions[index].rows = append(partitions[index].rows, workRow{
			partitionKey: key,
			sortValue:    row.SortValue,
			rowID:        row.RowID,
		})
	}

	sort.Slice(partitions, func(i, j int) bool {
		return partitions[i].key < partitions[j].key
	})
	return partitions, skipped
}
