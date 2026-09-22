// Package dict 维护 term → postings.List 的词典，支持精确与前缀查找。
package dict

import "ontology/postings"

// Dict 是 term 到倒排表的索引。不变量 1：每个 term 的倒排表非空。
type Dict struct {
	terms map[string]*postings.List
}

// New 创建空词典。
func New() *Dict {
	return &Dict{terms: make(map[string]*postings.List)}
}

// Get 精确查找。
func (d *Dict) Get(term string) (*postings.List, bool) { return nil, false }

// GetOrAdd 取已有倒排表，没有则创建空表。
func (d *Dict) GetOrAdd(term string) *postings.List { return nil }

// Delete 删除一个 term。
func (d *Dict) Delete(term string) {}

// PrefixSearch 返回所有以 prefix 开头的 term（字典序）。
func (d *Dict) PrefixSearch(prefix string) []string { return nil }

// SelfCheck 核验不变量 1：每个 term 的倒排表非空，且内部有序。
func (d *Dict) SelfCheck() error { return nil }
