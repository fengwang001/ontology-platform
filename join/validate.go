package join

// validateKeyTypes 校验每个连接键列在左右两侧的类型是否可比。
// 规则：不支持的类型报 ErrUnsupportedKeyType；同一列出现两个不同的
// 类型族（数值 / string / bool）报 ErrKeyTypeConflict；int64 与 float64
// 同属数值族，互可比。
func validateKeyTypes(keys []string, lk, rk sideKeys) error {
	for col, key := range keys {
		lf, lft, err := columnFamily(lk, col, key, true)
		if err != nil {
			return err
		}
		rf, rft, err := columnFamily(rk, col, key, false)
		if err != nil {
			return err
		}
		if lf != "" && rf != "" && lf != rf {
			return &KeyTypeError{Key: key, LeftType: lft, RightType: rft}
		}
	}
	return nil
}

// columnFamily 返回某一键列在一侧的类型族（"num"/"string"/"bool"）
// 及代表类型名；该侧该列全部为空时返回空串。发现不支持的类型或
// 同侧混族时返回错误。
func columnFamily(sk sideKeys, col int, key string, isLeft bool) (string, string, error) {
	family := ""
	typeName := ""
	for _, vals := range sk.cols {
		kv := vals[col]
		if kv.empty {
			continue
		}
		if kv.val.isUnsupported() {
			return "", "", &KeyTypeError{Key: key, LeftType: kv.val.typeName(), Unsupported: true}
		}
		f, t := familyOf(kv.val), kv.val.typeName()
		if family == "" {
			family, typeName = f, t
			continue
		}
		if family != f {
			err := &KeyTypeError{Key: key, LeftType: typeName, RightType: t, SameSide: true}
			if !isLeft {
				err.LeftType, err.RightType = t, typeName
			}
			return "", "", err
		}
	}
	return family, typeName, nil
}

func familyOf(c canonVal) string {
	switch {
	case c.numeric():
		return "num"
	case c.kind == kindString:
		return "string"
	default:
		return "bool"
	}
}
