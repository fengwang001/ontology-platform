package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

// trieNode 是某一次提交的不可变文件树节点。
// 叶子节点 isFile=true 且 children==nil；中间节点为目录。
type trieNode struct {
	name     string
	isFile   bool
	children map[string]*trieNode
	hash     string
}

// Commit 是一次已注册的提交：由文件路径列表构建的不可变树。
type Commit struct {
	ID   string
	root *trieNode
}

var errMalformedPath = errors.New("malformed path: empty segment")

// splitPath 按分隔符分段；空段（前导/尾随/连续分隔符）交给调用方裁决。
func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func joinSegs(segs []string) string { return strings.Join(segs, "/") }

func childPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// buildCommit 由文件相对路径列表构建提交树。
func buildCommit(id string, files []string) (*Commit, error) {
	root := &trieNode{children: map[string]*trieNode{}}
	seen := map[string]bool{}
	for _, f := range files {
		segs := splitPath(f)
		if len(segs) == 0 {
			return nil, errMalformedPath
		}
		for _, s := range segs {
			if s == "" {
				return nil, errMalformedPath
			}
		}
		if seen[f] {
			return nil, errors.New("duplicate path in commit: " + f)
		}
		seen[f] = true
		node := root
		for i, s := range segs {
			next, ok := node.children[s]
			if !ok {
				next = &trieNode{name: s, children: map[string]*trieNode{}}
				node.children[s] = next
			}
			if next.isFile {
				return nil, errors.New("path is both file and directory: " + f)
			}
			if i == len(segs)-1 {
				if len(next.children) > 0 {
					return nil, errors.New("path is both file and directory: " + f)
				}
				next.isFile = true
				next.children = nil
			}
			node = next
		}
	}
	computeHashes(root)
	return &Commit{ID: id, root: root}, nil
}

// computeHashes 自底向上计算子树内容指纹：
// 两个节点哈希相同，当且仅当其下的目录结构（子段名与文件/目录形态）完全一致。
func computeHashes(n *trieNode) string {
	if n.isFile {
		n.hash = "F"
		return n.hash
	}
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	h.Write([]byte("D"))
	for _, name := range names {
		ch := computeHashes(n.children[name])
		h.Write([]byte{byte(len(name) >> 8), byte(len(name))})
		h.Write([]byte(name))
		h.Write([]byte(ch))
		h.Write([]byte{0})
	}
	n.hash = hex.EncodeToString(h.Sum(nil))
	return n.hash
}

// subtreeHash 返回节点子树的内容指纹，用于提交间结构剪枝。
func (n *trieNode) subtreeHash() string { return n.hash }

// findNode 按分段定位节点，不存在返回 nil。
func findNode(root *trieNode, segs []string) *trieNode {
	node := root
	for _, s := range segs {
		if node == nil || node.children == nil {
			return nil
		}
		node = node.children[s]
		if node == nil {
			return nil
		}
	}
	return node
}

// allFiles 收集提交内全部文件路径，按字节序。
func allFiles(root *trieNode) []string {
	var out []string
	collectFiles(root, "", &out)
	sort.Strings(out)
	return out
}

func collectFiles(n *trieNode, dir string, out *[]string) {
	if n.isFile {
		*out = append(*out, dir)
		return
	}
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		collectFiles(n.children[name], childPath(dir, name), out)
	}
}
