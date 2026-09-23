// Package source 读取并规范化四类配置来源：默认值、文件、环境变量、命令行。
package source

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Layer 标记来源层，数值越小优先级越低。
type Layer int

const (
	LayerDefault Layer = iota
	LayerFile
	LayerEnv
	LayerArgs
)

func (l Layer) String() string {
	switch l {
	case LayerDefault:
		return "default"
	case LayerFile:
		return "file"
	case LayerEnv:
		return "env"
	case LayerArgs:
		return "args"
	}
	return "unknown"
}

// Entry 是一条规范化后的配置项：键路径 + 原始值 + 来源标记。
type Entry struct {
	Key   string
	Value string
	Layer Layer
}

var (
	// ErrEmptyKey 表示键为空串。
	ErrEmptyKey = errors.New("source: empty key")
	// ErrEmptySegment 表示键路径含空段（如 a..b）。
	ErrEmptySegment = errors.New("source: empty segment in key")
)

// NormalizeKey 校验并规范化键路径：去两侧空白、非空、无空段，深度不限。
func NormalizeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", ErrEmptyKey
	}
	for _, seg := range strings.Split(key, ".") {
		if seg == "" {
			return "", fmt.Errorf("%w: %q", ErrEmptySegment, key)
		}
	}
	return key, nil
}

// Map 把一个字面量映射（默认值或命令行键值）转成条目，按键排序保证确定性。
func Map(layer Layer, m map[string]string) ([]Entry, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	entries := make([]Entry, 0, len(keys))
	for _, k := range keys {
		key, err := NormalizeKey(k)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Key: key, Value: m[k], Layer: layer})
	}
	return entries, nil
}

// Args 解析命令行风格的 k=v、-k=v、--k=v 参数，值为空串合法。
func Args(args []string) ([]Entry, error) {
	entries := make([]Entry, 0, len(args))
	for _, a := range args {
		a = strings.TrimLeft(a, "-")
		key, value, found := strings.Cut(a, "=")
		if !found {
			return nil, fmt.Errorf("%w: arg %q has no '='", ErrEmptyKey, a)
		}
		key, err := NormalizeKey(key)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Key: key, Value: value, Layer: LayerArgs})
	}
	return entries, nil
}
