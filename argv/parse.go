package argv

import "fmt"

// parser holds the per-parse working state.
type parser struct {
	byLong  map[string]*Spec
	byShort map[rune]*Spec
	res     *Result
}

// Parse matches args against specs and returns the outcome.
//
// Parsing rules:
//   - "--name=value" and "--name value" are equivalent; so are
//     "-n value" and "-nvalue".
//   - Boolean short flags may be bundled ("-abc"); once a String
//     short flag appears in a bundle, the rest of the token is its
//     value.
//   - A lone "--" terminates flag parsing; everything after it is a
//     positional operand. A lone "-" is an operand too.
//   - Operands may be interleaved with flags and keep their order.
//
// Parse never mutates specs or args, and each call is independent.
// On any error it returns a nil Result together with an error that
// matches one of the package's sentinel errors via errors.Is.
func Parse(specs []Spec, args []string) (*Result, error) {
	p := &parser{
		byLong:  make(map[string]*Spec, len(specs)),
		byShort: make(map[rune]*Spec, len(specs)),
		res: &Result{
			bools:   make(map[string]bool, len(specs)),
			strings: make(map[string]string, len(specs)),
			set:     make(map[string]bool, len(specs)),
		},
	}
	for i := range specs {
		s := specs[i] // copy: never retain or mutate caller memory
		spec := &s
		p.byLong[spec.Long] = spec
		if spec.Short != 0 {
			p.byShort[spec.Short] = spec
		}
		if spec.Kind == String {
			p.res.strings[spec.Long] = spec.Default
		}
	}

	terminated := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case terminated:
			p.res.operands = append(p.res.operands, arg)
		case arg == "--":
			terminated = true
		case len(arg) > 2 && arg[0] == '-' && arg[1] == '-':
			if err := p.longFlag(arg[2:], args, &i); err != nil {
				return nil, err
			}
		case len(arg) > 1 && arg[0] == '-':
			if err := p.shortFlags(arg[1:], args, &i); err != nil {
				return nil, err
			}
		default: // lone "-" and plain words are operands
			p.res.operands = append(p.res.operands, arg)
		}
	}

	for _, s := range p.byLong {
		if s.Required && !p.res.set[s.Long] {
			return nil, fmt.Errorf("%w: --%s", ErrRequired, s.Long)
		}
	}
	return p.res, nil
}

// longFlag handles a "--name[=value]" token. body is the token with
// the leading "--" already stripped.
func (p *parser) longFlag(body string, args []string, i *int) error {
	name, value, hasValue := body, "", false
	for j := 0; j < len(body); j++ {
		if body[j] == '=' {
			name, value, hasValue = body[:j], body[j+1:], true
			break
		}
	}
	spec, ok := p.byLong[name]
	if !ok {
		return fmt.Errorf("%w: --%s", ErrUnknownFlag, name)
	}
	if err := p.checkDuplicate(spec); err != nil {
		return err
	}
	if spec.Kind == Bool {
		p.res.bools[spec.Long] = true
		return nil
	}
	if !hasValue {
		if *i+1 >= len(args) {
			return fmt.Errorf("%w: --%s", ErrMissingValue, spec.Long)
		}
		*i++
		value = args[*i]
	}
	p.res.strings[spec.Long] = value
	return nil
}

// shortFlags handles a "-xyz" token. body is the token with the
// leading "-" already stripped; it may bundle several flags.
func (p *parser) shortFlags(body string, args []string, i *int) error {
	runes := []rune(body)
	for j := 0; j < len(runes); j++ {
		spec, ok := p.byShort[runes[j]]
		if !ok {
			return fmt.Errorf("%w: -%c", ErrUnknownFlag, runes[j])
		}
		if err := p.checkDuplicate(spec); err != nil {
			return err
		}
		if spec.Kind == Bool {
			p.res.bools[spec.Long] = true
			continue
		}
		// String flag: the rest of the token is its value, or the
		// next argument if nothing remains.
		var value string
		if j+1 < len(runes) {
			value = string(runes[j+1:])
		} else {
			if *i+1 >= len(args) {
				return fmt.Errorf("%w: -%c", ErrMissingValue, spec.Short)
			}
			*i++
			value = args[*i]
		}
		p.res.strings[spec.Long] = value
		return nil
	}
	return nil
}

// checkDuplicate rejects a flag that was already explicitly set, and
// otherwise records it as set.
func (p *parser) checkDuplicate(spec *Spec) error {
	if p.res.set[spec.Long] {
		return fmt.Errorf("%w: --%s", ErrDuplicate, spec.Long)
	}
	p.res.set[spec.Long] = true
	return nil
}
