package par

import (
	"ontology/stream"
	"ontology/u8"
)

type Result struct {
	Output []byte
	Stats  stream.Stats
}

func Convert(data []byte, c stream.Config, k int) (Result, error) {
	if k < 1 {
		k = 1
	}
	if k > len(data) && len(data) > 0 {
		k = len(data)
	}
	type job struct {
		out []byte
		st  stream.Stats
		err error
		i   int
	}
	jobs := make(chan job, k)
	for i := 0; i < k; i++ {
		start, end := bounds(len(data), k, i)
		go func(i, start, end int) {
			prefix := 0
			if start > 0 {
				prefix = back(data[:start], c.From)
			}
			t := stream.Segment(c, prefix)
			n, err := t.Write(data[start-prefix : end])
			if err == nil {
				err = t.Close()
			}
			_ = n
			jobs <- job{t.Output(), t.Stats(), err, i}
		}(i, start, end)
	}
	parts := make([][]byte, k)
	var st stream.Stats
	var err error
	for i := 0; i < k; i++ {
		j := <-jobs
		parts[j.i] = j.out
		st.Scalars += j.st.Scalars
		st.Invalid += j.st.Invalid
		st.InvalidBytes += j.st.InvalidBytes
		st.BOMBytes += j.st.BOMBytes
		st.Consumed += j.st.Consumed
		st.Checks += j.st.Checks
		if j.err != nil && err == nil {
			err = j.err
		}
	}
	r := Result{Stats: st}
	for _, p := range parts {
		r.Output = append(r.Output, p...)
	}
	return r, err
}

func bounds(n, k, i int) (int, int) {
	return n * i / k, n * (i + 1) / k
}

func back(p []byte, f stream.Format) int {
	if f != stream.UTF8 {
		if len(p) > 0 && len(p)%2 == 1 {
			return 1
		}
		return 0
	}
	for i := len(p) - 1; i >= 0 && len(p)-i <= 3; i-- {
		b := p[i]
		if b < 0x80 || b >= 0xc0 {
			want := u8.LeadLen(b)
			if want == 1 {
				return 0
			}
			have := len(p) - i
			if have >= want {
				return 0
			}
			return have
		}
	}
	return min(3, len(p))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
