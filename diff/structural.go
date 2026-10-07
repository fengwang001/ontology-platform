package diff

// structural.go 承担结构层比对：对象类型与属性的增删改（含标识变化）。

func diffStructural(oldSnap, newSnap *Snapshot) (StructuralDiff, *CompareError) {
	return StructuralDiff{}, nil
}

// structuralAlignment 是结构层比对得出的口径描述，供实例层使用。
type structuralAlignment struct{}
