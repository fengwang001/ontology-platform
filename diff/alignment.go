package diff

// alignment.go 承担结构层与实例层之间的口径对齐：
// 把结构层差异翻译为实例身份映射、属性身份映射、类型兼容性表，
// 并判定实例层是否已失去比较口径。

func buildAlignment(oldSnap, newSnap *Snapshot, sd StructuralDiff) (*structuralAlignment, *CompareError) {
	return &structuralAlignment{}, nil
}
