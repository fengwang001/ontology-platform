package source

import (
	"fmt"
	"strings"
)

type Level int

const (
	Default Level = iota
	File
	Env
	Flag
)

func (l Level) String() string {
	switch l {
	case Default:
		return "default"
	case File:
		return "file"
	case Env:
		return "env"
	case Flag:
		return "flag"
	default:
		return "unknown"
	}
}

type Item struct {
	Key   string
	Value string
}

type Layer struct {
	Level Level
	Items []Item
}

func ValidateKey(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	for _, seg := range strings.Split(key, ".") {
		if seg == "" {
			return fmt.Errorf("%w: %q", ErrEmptySegment, key)
		}
	}
	return nil
}

func NewLayer(level Level, items map[string]string) (Layer, error) {
	keys := make([]string, 0, len(items))
	for key := range items {
		if err := ValidateKey(key); err != nil {
			return Layer{}, err
		}
		keys = append(keys, key)
	}
	sortStrings(keys)
	layer := Layer{Level: level, Items: make([]Item, 0, len(keys))}
	for _, key := range keys {
		layer.Items = append(layer.Items, Item{Key: key, Value: items[key]})
	}
	return layer, nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

func checkUnique(layer Layer) error {
	for i := 1; i < len(layer.Items); i++ {
		if layer.Items[i-1].Key == layer.Items[i].Key {
			return fmt.Errorf("%w: %q in %s layer",
				ErrDuplicateKey, layer.Items[i].Key, layer.Level)
		}
	}
	return nil
}
