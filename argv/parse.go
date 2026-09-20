package argv

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse parses args according to specs. On any error it returns a
// nil Result. It never mutates specs or args, and successive calls
// with the same specs are independent.
func Parse(specs []Spec, args []string) (*Result, error) {
	p := &parser{
		byLong:  make(map[string]Spec, len(specs)),
		byShort: make(map[rune]Spec, len(specs)),
		res: &Result{
			bools:    make(map[string]bool),
			strings:  make(map[string]string),
			set:      make(map[string]bool),
			defaults: make(map[string]string),
		},
	}
	for _, s := range specs {
		p.byLong[s.Long] = s
		if s.Short != 0 {
			p.byShort[s.Short] = s
		}
		if s.Kind == String {
			p.res.defaults[s.Long] = s.Default
		}
	}
	if err := p.parseArgs(args); err != nil {
		return nil, err
	}
	for _, s := range specs {
		if s.Required && !p.res.set[s.Long] {
			return nil, fmt.Errorf("%w: --%s", ErrRequired, s.Long)
		}
	}
	return p.res, nil
}

type parser struct {
	byLong  map[string]Spec
	byShort map[rune]Spec
	res     *Result
}

func (p *parser) parseArgs(args []string) error {
	terminated := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if terminated {
			p.res.operands = append(p.res.operands, arg)
			continue
		}
		switch {
		case arg == "--":
			terminated = true
		case arg == "-" || !strings.HasPrefix(arg, "-"):
			p.res.operands = append(p.res.operands, arg)
		case strings.HasPrefix(arg, "--"):
			if err := p.parseLong(arg[2:], args, &i); err != nil {
				return err
			}
		default:
			if err := p.parseShort(arg[1:], args, &i); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseLong handles one "--name[=value]" token. It may consume the
// next arg as the value, advancing i.
func (p *parser) parseLong(body string, args []string, i *int) error {
	name, value, hasValue := body, "", false
	if idx := strings.IndexByte(body, '='); idx >= 0 {
		name, value, hasValue = body[:idx], body[idx+1:], true
	}
	spec, ok := p.byLong[name]
	if !ok {
		return fmt.Errorf("%w: --%s", ErrUnknownFlag, name)
	}
	if spec.Kind == Bool {
		b := true
		if hasValue {
			v, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("argv: invalid bool value %q for --%s", value, name)
			}
			b = v
		}
		return p.setBool(spec, b)
	}
	if !hasValue {
		if *i+1 >= len(args) {
			return fmt.Errorf("%w: --%s", ErrMissingValue, name)
		}
		*i++
		value = args[*i]
	}
	return p.setString(spec, value)
}

// parseShort handles one "-xyz" token, which may merge several Bool
// shorts. A String short consumes the rest of the token, or the next
// arg if nothing remains, as its value.
func (p *parser) parseShort(body string, args []string, i *int) error {
	runes := []rune(body)
	for idx := 0; idx < len(runes); idx++ {
		spec, ok := p.byShort[runes[idx]]
		if !ok {
			return fmt.Errorf("%w: -%c", ErrUnknownFlag, runes[idx])
		}
		if spec.Kind == Bool {
			if err := p.setBool(spec, true); err != nil {
				return err
			}
			continue
		}
		value := string(runes[idx+1:])
		if value == "" {
			if *i+1 >= len(args) {
				return fmt.Errorf("%w: -%c", ErrMissingValue, runes[idx])
			}
			*i++
			value = args[*i]
		}
		if err := p.setString(spec, value); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func (p *parser) setBool(spec Spec, v bool) error {
	if err := p.markSet(spec); err != nil {
		return err
	}
	p.res.bools[spec.Long] = v
	return nil
}

func (p *parser) setString(spec Spec, v string) error {
	if err := p.markSet(spec); err != nil {
		return err
	}
	p.res.strings[spec.Long] = v
	return nil
}

func (p *parser) markSet(spec Spec) error {
	if p.res.set[spec.Long] {
		return fmt.Errorf("%w: --%s", ErrDuplicate, spec.Long)
	}
	p.res.set[spec.Long] = true
	return nil
}
