package apply

import (
	"encoding/base64"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"strconv"

	"ontology/name"
	"ontology/plan"
)

var ErrExecutionFailed = errors.New("execution failed")

type Applier struct {
	FailAt int
}

type Result struct {
	LogPath    string
	Applied    int
	RolledBack bool
	Undone     int
}

func (a Applier) Execute(ns *name.Namespace, steps []plan.Step, dir string) (*Result, error) {
	file, err := os.CreateTemp(dir, "rename-*.log")
	if err != nil {
		return nil, err
	}
	path := file.Name()

	res := &Result{LogPath: path}
	finish := func() (*Result, error) {
		file.Close()
		return res, nil
	}
	fail := func(err error) (*Result, error) {
		file.Close()
		for i := res.Applied - 1; i >= 0 && err != nil; i-- {
			ns.WithLock(func(items map[string]struct{}) {
				if !move(items, steps[i].To, steps[i].From) {
					err = fmt.Errorf("rollback failed at %d: %w", i, err)
				}
			})
			res.Undone++
		}
		os.Remove(path)
		return nil, err
	}
	ns.WithLock(func(items map[string]struct{}) {
		for _, step := range steps {
			if a.FailAt == res.Applied+1 {
				res.RolledBack = true
				err = fmt.Errorf("%w: step %d", ErrExecutionFailed, a.FailAt)
				return
			}
			if !move(items, step.From, step.To) {
				err = fmt.Errorf("%w: unsafe move %s→%s", ErrExecutionFailed, step.From, step.To)
				res.RolledBack = true
				return
			}
			if werr := WriteRecord(file, res.Applied, step); werr != nil {
				err = werr
				res.RolledBack = true
				return
			}
			res.Applied++
		}
	})
	if err == nil {
		return finish()
	}
	return fail(err)
}

func move(items map[string]struct{}, from, to string) bool {
	if from == to {
		_, ok := items[from]
		return ok
	}
	if _, exists := items[from]; !exists {
		return false
	}
	if _, busy := items[to]; busy {
		return false
	}
	delete(items, from)
	items[to] = struct{}{}
	return true
}

func EncodeRecord(index int, step plan.Step) []byte {
	payload := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(index) + "\x00" + step.From + "\x00" + step.To))
	record := []byte(payload + "\n")
	checksum := crc32.ChecksumIEEE(record)
	return []byte(fmt.Sprintf("%08x|%08x|%s", checksum, len(record), string(record)))
}

func WriteRecord(file *os.File, index int, step plan.Step) error {
	_, err := file.Write(EncodeRecord(index, step))
	return err
}
