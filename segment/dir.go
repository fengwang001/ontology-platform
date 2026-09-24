package segment

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// List 返回目录中按编号排序的段数据文件路径。
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "seg-") && strings.HasSuffix(name, ".log") {
			out = append(out, filepath.Join(dir, name))
		}
	}
	sort.Strings(out)
	return out, nil
}

// IndexPath 返回与段数据文件对应的索引文件路径。
func IndexPath(logPath string) string {
	return strings.TrimSuffix(logPath, ".log") + ".idx"
}
