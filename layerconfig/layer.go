package layerconfig

// Layer enumerates the four configuration layers from broad to narrow.
type Layer int

const (
	// LayerGlobal: no qualifier.
	LayerGlobal Layer = iota
	// LayerEnv: qualified by environment name.
	LayerEnv
	// LayerRegion: qualified by environment and region names.
	LayerRegion
	// LayerInstance: qualified by environment, region and instance names.
	LayerInstance
)

// String returns the stable name of the layer.
// String returns the stable name of the layer.
func (l Layer) String() string {
	switch l {
	case LayerGlobal:
		return "global"
	case LayerEnv:
		return "env"
	case LayerRegion:
		return "region"
	case LayerInstance:
		return "instance"
	default:
		return "invalid"
	}
}

// Scope qualifies a layer write. Empty names must be absent for the layer
// (see Valid).
type Scope struct {
	Env      string
	Region   string
	Instance string
}

// LayerOf returns the layer implied by which qualifier names are set.
func (s Scope) LayerOf() Layer { l, _ := s.Valid(); return l }

// Valid reports whether the scope is a legal layer qualifier and, if so, its
// layer.
// Valid reports whether the scope is a legal layer qualifier and, if so, its
// layer.
//
// global requires all names empty; env requires Env set and the rest empty;
// region requires Env and Region set, Instance empty; instance requires all
// three. A lower slot may never be set while an upper slot is empty.
func (s Scope) Valid() (Layer, bool) {
	switch {
	case s.Env == "" && s.Region == "" && s.Instance == "":
		return LayerGlobal, true
	case s.Env != "" && s.Region == "" && s.Instance == "":
		return LayerEnv, true
	case s.Env != "" && s.Region != "" && s.Instance == "":
		return LayerRegion, true
	case s.Env != "" && s.Region != "" && s.Instance != "":
		return LayerInstance, true
	default:
		return LayerGlobal, false
	}
}

// applicableScopes returns the broad-to-narrow chain of scopes visible when
// resolving the target scope. target need not be a full instance scope.
// applicableScopes returns the broad-to-narrow chain of scopes visible when
// resolving the target scope. target need not be a full instance scope.
func applicableScopes(target Scope) ([]Scope, error) {
	layer, ok := target.Valid()
	if !ok {
		return nil, errInvalid("invalid target scope: env=%q region=%q instance=%q", target.Env, target.Region, target.Instance)
	}
	chain := []Scope{{}}
	if layer >= LayerEnv {
		chain = append(chain, Scope{Env: target.Env})
	}
	if layer >= LayerRegion {
		chain = append(chain, Scope{Env: target.Env, Region: target.Region})
	}
	if layer >= LayerInstance {
		chain = append(chain, target)
	}
	return chain, nil
}

// scopedKey encodes a scope and key into a collision-free flat key. Names do
// not contain NUL, so NUL-delimited concatenation is injective.
func scopedKey(s Scope, key string) string {
	return s.Env + "\x00" + s.Region + "\x00" + s.Instance + "\x00" + key
}

// existenceKey encodes an environment / region / instance identity used to
// track which scopes have ever appeared in writes.
func existenceKey(env, region, instance string) string {
	return env + "\x00" + region + "\x00" + instance
}

// narrowerThan reports whether layer a is strictly narrower than layer b.
func narrowerThan(a, b Layer) bool { return a > b }

// parseScopedKey inverts scopedKey, returning the scope, key and layer.
func parseScopedKey(fk string) (Scope, Layer, bool) {
	i1 := indexByte(fk, 0)
	if i1 < 0 {
		return Scope{}, LayerGlobal, false
	}
	rest1 := fk[i1+1:]
	i2 := indexByte(rest1, 0)
	if i2 < 0 {
		return Scope{}, LayerGlobal, false
	}
	rest2 := rest1[i2+1:]
	i3 := indexByte(rest2, 0)
	if i3 < 0 {
		return Scope{}, LayerGlobal, false
	}
	sc := Scope{
		Env:      fk[:i1],
		Region:   rest1[:i2],
		Instance: rest2[:i3],
	}
	key := rest2[i3+1:]
	layer, ok := sc.Valid()
	if !ok || key == "" {
		return Scope{}, LayerGlobal, false
	}
	return sc, layer, true
}
