package rbac

import (
	"context"
	"log/slog"
)

type Effect int

const (
	Allow Effect = iota
	Deny
)

type Permission struct {
	Resource string
	Action   string
	Effect   Effect
}

func (p Permission) key() string { return GrantKey(p.Resource, p.Action) }

func (e Effect) String() string {
	if e == Deny {
		return "deny"
	}
	return "allow"
}

// Decision is the evaluated permission set of one subject.
// Allowed maps each resource/action key (see GrantKey) to whether it is
// allowed; Sources maps the same key to the basis of the decision: "direct"
// for a subject grant or the name of the role whose grant decided it.
type Decision struct {
	Subject string
	Allowed map[string]bool
	Sources map[string]string
}

// GrantKey builds the map key used by Decision.Allowed / Decision.Sources.
func GrantKey(resource, action string) string { return resource + ":" + action }

// Evaluate returns the effective permissions of subject. It takes only a read
// lock, so it may run concurrently with other evaluations and sees a single
// consistent snapshot.
func (m *Manager) Evaluate(ctx context.Context, subject string) Decision {
	m.mu.RLock()
	defer m.mu.RUnlock()

	roles := m.effectiveRoles(m.assignments[subject])
	roleNames := make([]string, 0, len(roles))
	for role := range roles {
		roleNames = append(roleNames, role)
	}
	sortStrings(roleNames)

	allowed := make(map[string]bool)
	sources := make(map[string]string)

	for _, role := range roleNames {
		for key, grant := range m.roles[role] {
			grantAllows := grant.Effect == Allow
			if existing, decided := allowed[key]; decided {
				// Role-vs-role conflicts resolve deny-overrides, which keeps
				// decisions independent of role registration order.
				if existing && !grantAllows {
					allowed[key] = false
					sources[key] = role
				}
				continue
			}
			allowed[key] = grantAllows
			sources[key] = role
		}
	}

	// Direct subject grants are applied last and always win over role grants.
	for key, grant := range m.subjects[subject] {
		allowed[key] = grant.Effect == Allow
		sources[key] = "direct"
	}

	keys := make([]string, 0, len(allowed))
	for key := range allowed {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		m.logger.LogAttrs(ctx, slog.LevelInfo, "permission evaluated",
			slog.String("subject", subject),
			slog.String("basis", sources[key]),
			slog.String("permission", key),
			slog.Bool("allowed", allowed[key]),
		)
	}

	return Decision{Subject: subject, Allowed: allowed, Sources: sources}
}

// IsAllowed reports whether subject may perform action on resource according
// to the current snapshot. Undefined permissions are denied.
func (m *Manager) IsAllowed(ctx context.Context, subject, resource, action string) bool {
	return m.Evaluate(ctx, subject).Allowed[GrantKey(resource, action)]
}
