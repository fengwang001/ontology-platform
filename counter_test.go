package ontology

import "testing"

func TestCurrentPostingReadsUnchangedByIrrelevantDocs(t *testing.T) {
	sizes := []int{1000, 100000}
	counts := make([]int64, len(sizes))

	for i, size := range sizes {
		idx := NewIndex()
		for docNumber := 0; docNumber < size; docNumber++ {
			if docNumber < 128 {
				if _, err := idx.Add(docName(docNumber), []string{"matched", "other"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := idx.Add(docName(docNumber), []string{"irrelevant"}); err != nil {
					t.Fatal(err)
				}
			}
		}

		if _, err := idx.Search([]string{"matched", "matched"}, 10, idx.Version()); err != nil {
			t.Fatal(err)
		}
		counts[i] = idx.CurrentPostingReads()
		if counts[i] != 128 {
			t.Fatalf("N=%d reads=%d, want 128", size, counts[i])
		}
		t.Logf("input=N=%d query=matched matched; output=posting reads %d; criterion=sum of unique query terms' current posting lengths and independent of unrelated docs", size, counts[i])
	}

	if counts[0] != counts[1] {
		t.Fatalf("read counts changed with irrelevant documents: %d vs %d", counts[0], counts[1])
	}
}

func docName(number int) string {
	const digits = "0123456789"
	if number == 0 {
		return "d0"
	}
	var reversed []byte
	for number > 0 {
		reversed = append(reversed, digits[number%10])
		number /= 10
	}
	name := make([]byte, 0, len(reversed)+1)
	name = append(name, 'd')
	for i := len(reversed) - 1; i >= 0; i-- {
		name = append(name, reversed[i])
	}
	return string(name)
}
