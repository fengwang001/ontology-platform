package persist

import (
	"ontology/grid"
)

// Save 原子写盘：先写 name+".tmp"，Sync 后改名；清理残留临时文件。
func Save(g *grid.Grid, name string) error { return nil }

// Load 读回网格。文件被截断时返回最大可恢复前缀及分类错误。
func Load(name string) (*grid.Grid, error) { return nil, nil }

// CleanupTemp 删除残留的写盘临时文件。
func CleanupTemp(name string) error { return nil }
