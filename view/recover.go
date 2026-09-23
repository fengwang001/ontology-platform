package view

import (
	"errors"
	"os"

	"ontology/journal"
)

// Open 打开（或创建）持久视图：重放日志中的完整记录重建状态，
// 截断的尾部被切除后可在原文件上继续追加。hook 为崩溃注入钩子。
func Open(path string, hook func(Stage)) (*View, error) {
	changes, validLen, err := journal.Replay(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		changes = nil
	case err != nil:
		var re *journal.ReplayError
		if !errors.As(err, &re) {
			return nil, err
		}
		if terr := os.Truncate(path, validLen); terr != nil {
			return nil, terr
		}
	}
	jw, err := journal.OpenAppend(path)
	if err != nil {
		return nil, err
	}
	v := newView(jw, hook)
	for _, c := range changes {
		v.applyMem(c)
	}
	return v, nil
}

// Close 关闭底层日志文件。
func (v *View) Close() error {
	if v.jw != nil {
		return v.jw.Close()
	}
	return nil
}
