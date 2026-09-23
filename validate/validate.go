// Package validate 对展开后的最终配置做类型校验与必填检查。
package validate

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ontology/source"
)

var (
	// ErrTypeMismatch 表示值无法解析为声明类型。
	ErrTypeMismatch = errors.New("validate: type mismatch")
	// ErrMissingRequired 表示必填键缺失，错误信息列出全部缺失键。
	ErrMissingRequired = errors.New("validate: missing required keys")
)

// Type 是声明的配置类型。
type Type string

const (
	String Type = "string"
	Int    Type = "int"
	Float  Type = "float"
	Bool   Type = "bool"
)

// Rule 描述一个键的类型与是否必填。
type Rule struct {
	Key      string
	Type     Type
	Required bool
}

// Check 校验全部规则，聚合所有违规后一次返回（errors.Is 可区分）。
// values 是展开后的最终表，layers 给出每个键的胜出层用于报错。
func Check(rules []Rule, values map[string]string, layers map[string]source.Layer) error {
	var errs []error
	var missing []string
	for _, rule := range rules {
		value, ok := values[rule.Key]
		if !ok {
			if rule.Required {
				missing = append(missing, rule.Key)
			}
			continue
		}
		if err := checkType(rule.Type, value); err != nil {
			errs = append(errs, fmt.Errorf("%w: key %q expects %s, got %q from layer %s",
				ErrTypeMismatch, rule.Key, rule.Type, value, layers[rule.Key]))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		errs = append(errs, fmt.Errorf("%w: %s", ErrMissingRequired, strings.Join(missing, ", ")))
	}
	return errors.Join(errs...)
}

func checkType(t Type, value string) error {
	var err error
	switch t {
	case String, "":
		return nil
	case Int:
		_, err = strconv.ParseInt(value, 10, 64)
	case Float:
		_, err = strconv.ParseFloat(value, 64)
	case Bool:
		_, err = strconv.ParseBool(value)
	default:
		return fmt.Errorf("validate: unknown type %q", t)
	}
	return err
}
