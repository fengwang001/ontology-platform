package dceerr

import "fmt"

// Category 是错误类别。
type Category int

const (
	CatInvalidArgument    Category = iota + 1 // 非法参数（空标识 / 重名）
	CatDuplicateModule                        // 重复模块
	CatUnknownModule                          // 未知模块
	CatUndefinedReference                     // 未定义引用
	CatMissingExport                          // 缺失导出（解析检查）
	CatAmbiguousExport                        // 歧义导出（解析检查）
	CatReexportCycle                          // 重导出循环（解析检查）
)

func (c Category) String() string {
	switch c {
	case CatInvalidArgument:
		return "invalid argument"
	case CatDuplicateModule:
		return "duplicate module"
	case CatUnknownModule:
		return "unknown module"
	case CatUndefinedReference:
		return "undefined reference"
	case CatMissingExport:
		return "missing export"
	case CatAmbiguousExport:
		return "ambiguous export"
	case CatReexportCycle:
		return "reexport cycle"
	default:
		return "unknown error"
	}
}

// Error 携带可机读的错误类别与定位信息。
type Error struct {
	Category Category
	Module   string // 涉及的本模块（导入/重导出语句所在模块；重复模块时为模块标识）
	Target   string // 目标模块标识（未知模块、重导出链）
	Name     string // 导出名 / 声明名 / 标识符
	Detail   string // 人类可读补充信息
}

func (e *Error) Error() string {
	switch e.Category {
	case CatInvalidArgument, CatDuplicateModule, CatUndefinedReference:
		return fmt.Sprintf("%s: module=%q name=%q: %s", e.Category, e.Module, e.Name, e.Detail)
	default:
		return fmt.Sprintf("%s: module=%q target=%q name=%q: %s", e.Category, e.Module, e.Target, e.Name, e.Detail)
	}
}

// As 从 error 中提取 *Error。
func As(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	if e, ok := err.(*Error); ok {
		return e, true
	}
	return nil, false
}
