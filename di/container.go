package di

import (
	"errors"
	"fmt"
	"sync"
)

// Constructor builds a service instance. deps maps declared dependency
// names to instances resolved for that service. Release (possibly nil)
// is invoked exactly once by the owning container/scope, after every
// instance that depends on it has been released.
type Constructor func(deps map[string]any) (instance any, release func())

// Lifetime of a registration.
type Lifetime int

const (
	// Singleton services are constructed at most once successfully per
	// container and are released by container shutdown.
	Singleton Lifetime = iota
	// Scoped services are constructed at most once successfully per scope.
	Scoped
	// Transient services are constructed on every resolution.
	Transient
)

func (l Lifetime) String() string {
	switch l {
	case Singleton:
		return "singleton"
	case Scoped:
		return "scoped"
	case Transient:
		return "transient"
	default:
		return "unknown"
	}
}

// Sentinel errors. Constructor failures are wrapped in ConstructionError.
var (
	ErrDuplicateRegistration = errors.New("di: duplicate registration")
	ErrDependencyNotFound    = errors.New("di: dependency not registered")
	ErrDependencyCycle       = errors.New("di: dependency cycle detected")
	ErrCaptiveDependency     = errors.New("di: lifetime captive dependency detected")
	ErrServiceNotFound       = errors.New("di: service not registered")
	ErrScopedFromRoot        = errors.New("di: cannot resolve scoped service from root container")
	ErrFrozen                = errors.New("di: container is frozen")
	ErrNotFrozen             = errors.New("di: container is not frozen")
	ErrContainerClosed       = errors.New("di: container is closed")
	ErrScopeClosed           = errors.New("di: scope is closed")
)

// ConstructionError reports a constructor failure during resolution.
type ConstructionError struct {
	Service string
	Err     error
}

func (e *ConstructionError) Error() string {
	return "di: construction of " + e.Service + " failed: " + e.Err.Error()
}

func (e *ConstructionError) Unwrap() error { return e.Err }

// registration is a single service registration.
type registration struct {
	name     string
	deps     []string
	lifetime Lifetime
	ctor     Constructor
}

// record tracks one constructed instance owned by a container or scope.
type record struct {
	name    string
	value   any
	release func()
}

// inflight is a single-flight record for a currently building singleton
// or scoped instance.
type inflight struct {
	done chan struct{}
	val  any
	err  error
}

// Container is a dependency injection container with lifecycle management.
// The zero value is not usable; use New.
type Container struct {
	mu        sync.Mutex
	regs      map[string]*registration
	order     []string // registration order for deterministic validation
	dups      []string // names registered more than once, first offender first
	retryMode bool     // next registration starts a corrected batch
	frozen    bool
	closed    bool
	singles   map[string]any
	building  map[string]*inflight
	owned     []record // singletons + root-owned transients, creation order
	active    int      // in-flight root resolutions
	closing   bool
	done      chan struct{} // closed once owned instances are released
	cond      *sync.Cond
	scopesMu  sync.Mutex
	scopes    map[*Scope]struct{}
}

// Scope is a child resolution scope.
type Scope struct {
	container *Container

	mu       sync.Mutex
	scoped   map[string]any
	building map[string]*inflight
	owned    []record // scoped instances + scope-owned transients
	active   int
	closing  bool
	closed   bool
	done     chan struct{}
	cond     *sync.Cond
}

// New creates an unfrozen container accepting registrations.
func New() *Container {
	c := &Container{
		regs:     make(map[string]*registration),
		singles:  make(map[string]any),
		building: make(map[string]*inflight),
		scopes:   make(map[*Scope]struct{}),
		done:     make(chan struct{}),
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Register adds a service registration before Freeze. A name used more
// than once is retained and reported by Freeze before any other problem.
func (c *Container) Register(name string, deps []string, lifetime Lifetime, ctor Constructor) error {
	if name == "" {
		return errors.New("di: service name must not be empty")
	}
	if ctor == nil {
		return fmt.Errorf("di: constructor for %q must not be nil", name)
	}
	if lifetime < Singleton || lifetime > Transient {
		return fmt.Errorf("di: invalid lifetime %d for %q", lifetime, name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrContainerClosed
	}
	if c.frozen {
		return fmt.Errorf("%w: %s", ErrFrozen, name)
	}
	depCopy := append([]string(nil), deps...)
	if _, ok := c.regs[name]; !ok {
		c.order = append(c.order, name)
	} else {
		if !c.retryMode {
			c.dups = append(c.dups, name)
		}
	}
	c.regs[name] = &registration{
		name:     name,
		deps:     depCopy,
		lifetime: lifetime,
		ctor:     ctor,
	}
	c.retryMode = false
	return nil
}

// Freeze validates registrations and locks the container. Validation order
// is: duplicate registration, unregistered dependency, dependency cycle,
// lifetime captivity. The first violation only is returned and the whole
// configuration is rejected; the container stays open for re-registration.
func (c *Container) Freeze() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrContainerClosed
	}
	if c.frozen {
		c.mu.Unlock()
		return nil
	}
	regs := make(map[string]*registration, len(c.regs))
	order := append([]string(nil), c.order...)
	dups := append([]string(nil), c.dups...)
	for k, v := range c.regs {
		cp := *v
		cp.deps = append([]string(nil), v.deps...)
		regs[k] = &cp
	}
	c.mu.Unlock()

	if err := validate(regs, order, dups); err != nil {
		c.mu.Lock()
		c.dups = nil
		c.retryMode = true
		c.mu.Unlock()
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrContainerClosed
	}
	c.frozen = true
	return nil
}

// validate performs the ordered static checks over a snapshot.
func validate(regs map[string]*registration, order, dups []string) error {
	// 1. Duplicate registration (first name seen twice in input order).
	if len(dups) > 0 {
		return fmt.Errorf("%w: %s", ErrDuplicateRegistration, dups[0])
	}

	// 2. Unregistered dependency (first offender in registration order).
	for _, name := range order {
		for _, dep := range regs[name].deps {
			if _, ok := regs[dep]; !ok {
				return fmt.Errorf("%w: service %s requires %s", ErrDependencyNotFound, name, dep)
			}
		}
	}

	// 3. Dependency cycle (first back edge in registration order).
	const (
		colorWhite = 0
		colorGray  = 1
		colorBlack = 2
	)
	color := make(map[string]int, len(order))
	var stack []string
	var dfsCycle func(string) []string
	dfsCycle = func(name string) []string {
		color[name] = colorGray
		stack = append(stack, name)
		for _, dep := range regs[name].deps {
			switch color[dep] {
			case colorGray:
				for i, n := range stack {
					if n == dep {
						return append(append([]string(nil), stack[i:]...), dep)
					}
				}
			case colorWhite:
				if cyc := dfsCycle(dep); cyc != nil {
					return cyc
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = colorBlack
		return nil
	}
	for _, name := range order {
		if color[name] == colorWhite {
			if cyc := dfsCycle(name); cyc != nil {
				return fmt.Errorf("%w: %v", ErrDependencyCycle, cyc)
			}
		}
	}

	// 4. Lifetime captivity. From each singleton the dependency closure
	// is expanded through transients and other singletons are treated as
	// boundaries (their closure is their own singleton's responsibility);
	// a scoped service anywhere in the reachable closure is forbidden.
	checked := make(map[string]bool, len(order))
	var checkCaptive func(start string) (string, bool)
	checkCaptive = func(name string) (string, bool) {
		for _, dep := range regs[name].deps {
			depReg := regs[dep]
			switch depReg.lifetime {
			case Scoped:
				return dep, true
			case Transient:
				if !checked[dep] {
					if bad, ok := checkCaptive(dep); ok {
						return bad, true
					}
				}
			case Singleton:
				// Boundary: the singleton validates its own closure.
			}
		}
		checked[name] = true
		return "", false
	}
	for _, name := range order {
		if regs[name].lifetime == Singleton {
			if bad, ok := checkCaptive(name); ok {
				return fmt.Errorf("%w: singleton %s captures scoped %s via its closure",
					ErrCaptiveDependency, name, bad)
			}
		}
	}

	return nil
}
