// Package lagord maintains one partition's rows ordered by (Sort, ID).
package lagord

// Row is one stored row inside a partition.
type Row struct {
	ID   int64
	Sort int64
	Val  int64
}

// Seq is an ordered row sequence for a single partition.
type Seq struct {
	rows []Row
	cmp  int // rows compared while locating in the latest insert/delete; unexported on purpose
}

func less(a, b Row) bool {
	if a.Sort != b.Sort {
		return a.Sort < b.Sort
	}
	return a.ID < b.ID
}

// Len returns the row count.
func (s *Seq) Len() int { return len(s.rows) }

// At returns the i-th row in (Sort, ID) order.
func (s *Seq) At(i int) Row { return s.rows[i] }

// Locate binary-searches the key (sort, id) and returns the insertion
// index plus whether a row with exactly that key exists.
func (s *Seq) Locate(sort, id int64) (int, bool) {
	s.cmp = 0
	key := Row{Sort: sort, ID: id}
	lo, hi := 0, len(s.rows)
	for lo < hi {
		s.cmp++
		mid := int(uint(lo+hi) >> 1)
		if less(s.rows[mid], key) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, lo < len(s.rows) && s.rows[lo].Sort == sort && s.rows[lo].ID == id
}

// Insert places r at its ordered position and returns that index.
func (s *Seq) Insert(r Row) int {
	i, _ := s.Locate(r.Sort, r.ID)
	s.rows = append(s.rows, Row{})
	copy(s.rows[i+1:], s.rows[i:])
	s.rows[i] = r
	return i
}

// RemoveAt deletes the i-th row and returns it.
func (s *Seq) RemoveAt(i int) Row {
	r := s.rows[i]
	s.rows = append(s.rows[:i], s.rows[i+1:]...)
	return r
}
