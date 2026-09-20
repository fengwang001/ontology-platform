package ontology

import "fmt"

// extractKey reads the join key tuple from a row. null is true when any key
// attribute is missing, nil, or NaN; such rows never match anything.
func extractKey(r Row, keys []string) (tuple []keyVal, null bool, err error) {
	tuple = make([]keyVal, len(keys))
	for i, name := range keys {
		v, present := r[name]
		if !present {
			return nil, true, nil
		}
		kv, ok, err := classify(v)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, true, nil
		}
		tuple[i] = kv
	}
	return tuple, false, nil
}

// validateKeyTypes ensures every key uses one comparability category across
// both tables. NULL values are ignored. It returns a *KeyTypeError naming
// the key and the conflicting left/right Go types on mismatch.
func validateKeyTypes(left, right []Row, keys []string) error {
	for _, name := range keys {
		leftCat, leftType, err := sideKeyType(left, name)
		if err != nil {
			return err
		}
		rightCat, rightType, err := sideKeyType(right, name)
		if err != nil {
			return err
		}
		if leftType != "" && rightType != "" && leftCat != rightCat {
			return &KeyTypeError{Key: name, LeftType: leftType, RightType: rightType}
		}
	}
	return nil
}

// sideKeyType finds the comparability category of one key on one side and a
// representative Go type name. Mixed categories within one side are an
// error; the zero type name means no non-NULL value was seen.
func sideKeyType(rows []Row, name string) (keyCat, string, error) {
	var cat keyCat
	typeName := ""
	for _, r := range rows {
		v, present := r[name]
		if !present {
			continue
		}
		kv, ok, err := classify(v)
		if err != nil {
			return cat, "", err
		}
		if !ok {
			continue
		}
		t := fmt.Sprintf("%T", v)
		if typeName == "" {
			cat, typeName = kv.cat, t
			continue
		}
		if kv.cat != cat {
			return cat, "", &KeyTypeError{Key: name, LeftType: typeName, RightType: t}
		}
	}
	return cat, typeName, nil
}
