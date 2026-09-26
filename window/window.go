package window

type Ring struct {
	capacity int
}

func New(capacity int) (*Ring, error) {
	return &Ring{capacity: capacity}, nil
}

func (r *Ring) Capacity() int { return r.capacity }

func (r *Ring) Len() int { return 0 }

func (r *Ring) Add(p []byte) {}

func (r *Ring) At(distance int) byte { return 0 }
