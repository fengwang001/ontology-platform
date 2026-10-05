// Package ontology 提供点播转码作业的阶梯推导、档位任务与清单分版发布门面。
package ontology

import "errors"

var (
	// ErrInvalidParam 参数非法（id/rung 为空、now 越界、源参数越界、size 越界等）。
	ErrInvalidParam = errors.New("ontology: 参数非法")
	// ErrClockSkew 时钟回退：now 小于已接受操作的最大 now。
	ErrClockSkew = errors.New("ontology: 时钟回退")
	// ErrJobExists 作业已存在。
	ErrJobExists = errors.New("ontology: 作业已存在")
	// ErrJobNotFound 作业不存在。
	ErrJobNotFound = errors.New("ontology: 作业不存在")
	// ErrJobFailed 作业已失败（任一 required 档终败）。
	ErrJobFailed = errors.New("ontology: 作业失败")
)
