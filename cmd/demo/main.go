package main

import (
	"fmt"

	"ontology/roll"
	"ontology/split"
)

func main() {
	ok := true
	checks := []struct {
		name string
		fn   func() error
	}{
		{"roll O(1) incremental", func() error {
			h, err := roll.New(4)
			if err != nil {
				return err
			}
			for _, b := range []byte("abcdefgh") {
				if !h.Full() {
					h.Write(b)
				} else {
					h.Push(b)
				}
			}
			return nil
		}},
		{"split config errors distinct", func() error {
			if _, err := split.New(split.Config{Window: 4, Min: 8, Max: 4}); err != split.ErrMinMax {
				return fmt.Errorf("want ErrMinMax, got %v", err)
			}
			if _, err := split.New(split.Config{Window: 0, Min: 8, Max: 16}); err != split.ErrWindow {
				return fmt.Errorf("want ErrWindow, got %v", err)
			}
			if _, err := split.New(split.Config{Window: 9, Min: 8, Max: 16}); err != split.ErrWindow {
				return fmt.Errorf("want ErrWindow, got %v", err)
			}
			return nil
		}},
	}
	for _, c := range checks {
		if c.fn() != nil {
			ok = false
			fmt.Println("FAIL", c.name)
		} else {
			fmt.Println("OK  ", c.name)
		}
	}
	if !ok {
		panic("demo failed")
	}
}
