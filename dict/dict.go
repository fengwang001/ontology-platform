package dict

import "errors"

var ErrNotFound = errors.New("dict: value not found")

type Int64 struct {
	index map[int64]uint64
	items []int64
}

func NewInt64(values []int64) *Int64 {
	d := &Int64{index: make(map[int64]uint64)}
	for _, value := range values {
		if _, ok := d.index[value]; !ok {
			d.index[value] = uint64(len(d.items))
			d.items = append(d.items, value)
		}
	}
	return d
}

func (d *Int64) Code(value int64) (uint64, bool) {
	code, ok := d.index[value]
	return code, ok
}

func (d *Int64) Value(code uint64) (int64, error) {
	if int(code) >= len(d.items) {
		return 0, ErrNotFound
	}
	return d.items[code], nil
}

func (d *Int64) Len() int { return len(d.items) }

func (d *Int64) Values() []int64 { return append([]int64(nil), d.items...) }

type String struct {
	index map[string]uint64
	items []string
}

func NewString(values []string) *String {
	d := &String{index: make(map[string]uint64)}
	for _, value := range values {
		if _, ok := d.index[value]; !ok {
			d.index[value] = uint64(len(d.items))
			d.items = append(d.items, value)
		}
	}
	return d
}

func (d *String) Code(value string) (uint64, bool) {
	code, ok := d.index[value]
	return code, ok
}

func (d *String) Value(code uint64) (string, error) {
	if int(code) >= len(d.items) {
		return "", ErrNotFound
	}
	return d.items[code], nil
}

func (d *String) Len() int { return len(d.items) }

func (d *String) Values() []string { return append([]string(nil), d.items...) }
