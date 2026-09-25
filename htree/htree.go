// Package htree 构建 Huffman 树：确定性并列打破，产出码长与左0右1码字。
package htree

import (
	"errors"
	"sort"
)

// ErrInvalidFreq 表示频次表为空、全零或含负频次。
var ErrInvalidFreq = errors.New("htree: invalid frequency table")

type node struct {
	freq  int
	name  byte // 子树内字典序最小的符号
	sym   byte // 仅叶子有效
	leaf  bool
	left  *node
	right *node
}

// build 校验频次表并返回根节点；单符号时返回该叶子本身。
func build(freq map[byte]int) (*node, error) {
	if len(freq) == 0 {
		return nil, ErrInvalidFreq
	}
	var nodes []*node
	total := 0
	for s, f := range freq {
		if f < 0 {
			return nil, ErrInvalidFreq
		}
		total += f
		if f > 0 { // 零频次符号不进树
			nodes = append(nodes, &node{freq: f, name: s, sym: s, leaf: true})
		}
	}
	if total == 0 {
		return nil, ErrInvalidFreq
	}
	for len(nodes) > 1 {
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].freq != nodes[j].freq {
				return nodes[i].freq < nodes[j].freq
			}
			return nodes[i].name < nodes[j].name
		})
		a, b := nodes[0], nodes[1] // 小者居左
		name := a.name
		if b.name < name {
			name = b.name
		}
		nodes = append(nodes[2:], &node{freq: a.freq + b.freq, name: name, left: a, right: b})
	}
	return nodes[0], nil
}

// Lengths 返回每个符号的码长（树深度）；单符号字母表码长为 1。
func Lengths(freq map[byte]int) (map[byte]int, error) {
	root, err := build(freq)
	if err != nil {
		return nil, err
	}
	out := map[byte]int{}
	var walk func(n *node, d int)
	walk = func(n *node, d int) {
		if n.leaf {
			if d == 0 {
				d = 1 // 单符号字母表
			}
			out[n.sym] = d
			return
		}
		walk(n.left, d+1)
		walk(n.right, d+1)
	}
	walk(root, 0)
	return out, nil
}

// Codes 返回按「左 0 右 1」直接赋值的码字（用于与规范码对照）。
func Codes(freq map[byte]int) (map[byte]string, error) {
	root, err := build(freq)
	if err != nil {
		return nil, err
	}
	out := map[byte]string{}
	var walk func(n *node, p string)
	walk = func(n *node, p string) {
		if n.leaf {
			if p == "" {
				p = "0" // 单符号字母表
			}
			out[n.sym] = p
			return
		}
		walk(n.left, p+"0")
		walk(n.right, p+"1")
	}
	walk(root, "")
	return out, nil
}
