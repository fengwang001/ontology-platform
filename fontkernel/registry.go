package fontkernel

// registry 保存全部已登记字体族。登记内容在构造后只读，
// 动态加载状态保存在 engine 中，二者分离以便并发控制。
type registry struct {
	families map[string]*family
	order    []string
}

// family 是一个已登记字体族及其静态字符覆盖索引。
type family struct {
	name  string
	faces []FaceSpec
	runes [][]faceRuneRange // 每张人脸归一化后的区间
	tree  *centroidTree
}

func newRegistry() *registry {
	return &registry{families: map[string]*family{}}
}

// validateFaceSpec 做纯参数合法性校验（拒绝次序中的“参数非法”）。
func validateFaceSpec(f FaceSpec) ([][]faceRuneRange, error) {
	if f.WeightLo > f.WeightHi || f.WeightLo <= 0 || f.WeightHi > 1000 {
		return nil, ErrInvalidArgument
	}
	if f.WidthLo > f.WidthHi || f.WidthLo <= 0 {
		return nil, ErrInvalidArgument
	}
	if f.Style != StyleNormal && f.Style != StyleItalic {
		return nil, ErrInvalidArgument
	}
	switch f.Policy {
	case PolicyBlock, PolicySwap, PolicyFallback, PolicyOptional:
	default:
		return nil, ErrInvalidArgument
	}
	if len(f.Runes) == 0 {
		return nil, ErrInvalidArgument
	}
	n, err := normalizeRanges(f.Runes)
	if err != nil {
		return nil, err
	}
	return [][]faceRuneRange{n}, nil
}

func sameCoverage(a, b []faceRuneRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// register 登记一个族。调用方保证已持有串行化锁。
func (r *registry) register(spec FamilySpec) error {
	if spec.Name == "" || len(spec.Faces) == 0 {
		return ErrInvalidArgument
	}
	// 先做完全部参数校验，再判断任何重复，保证“参数非法先于重复登记”。
	normalized := make([][]faceRuneRange, len(spec.Faces))
	for i, f := range spec.Faces {
		n, err := validateFaceSpec(f)
		if err != nil {
			return err
		}
		normalized[i] = n[0]
	}
	if _, exists := r.families[spec.Name]; exists {
		return ErrDuplicate
	}
	// 同族重复：字重区间、倾斜、宽度区间、字符范围四者完全相同。
	for i := 0; i < len(spec.Faces); i++ {
		for j := i + 1; j < len(spec.Faces); j++ {
			a, b := spec.Faces[i], spec.Faces[j]
			if a.WeightLo == b.WeightLo && a.WeightHi == b.WeightHi &&
				a.Style == b.Style &&
				a.WidthLo == b.WidthLo && a.WidthHi == b.WidthHi &&
				sameCoverage(normalized[i], normalized[j]) {
				return ErrDuplicate
			}
		}
	}

	fam := &family{name: spec.Name, faces: append([]FaceSpec(nil), spec.Faces...), runes: normalized}
	var all []faceRuneRange
	for i, n := range normalized {
		for _, rr := range n {
			rr.face = i
			all = append(all, rr)
		}
	}
	fam.tree = newCentroidTree(all)
	r.families[fam.name] = fam
	r.order = append(r.order, fam.name)
	return nil
}

func (r *registry) get(name string) (*family, bool) {
	f, ok := r.families[name]
	return f, ok
}

// coveringFaces 返回覆盖 r 的去重人脸上标，按登记序升序。
func (f *family) coveringFaces(r rune) []int {
	hits := f.tree.stab(r)
	if len(hits) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(hits))
	for _, h := range hits {
		seen[h] = struct{}{}
	}
	out := make([]int, 0, len(seen))
	for i := range f.faces {
		if _, ok := seen[i]; ok {
			out = append(out, i)
		}
	}
	return out
}
