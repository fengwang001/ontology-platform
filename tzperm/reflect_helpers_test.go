package tzperm

import "reflect"

func structFields(v any) string {
	rv := reflect.ValueOf(v)
	var out []byte
	walk(rv, &out)
	return string(out)
}

func walk(rv reflect.Value, out *[]byte) {
	switch rv.Kind() {
	case reflect.Ptr:
		if !rv.IsNil() {
			walk(rv.Elem(), out)
		}
	case reflect.Struct:
		typ := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			if !typ.Field(i).IsExported() {
				continue
			}
			*out = append(*out, typ.Field(i).Name...)
			*out = append(*out, '=')
			walk(rv.Field(i), out)
			*out = append(*out, ';')
		}
	default:
		*out = append(*out, rv.String()...)
	}
}
