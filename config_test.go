package ontology

import "testing"

func TestInvalidConfigRejected(t *testing.T) {
	valid := Config{
		AuthPendingTTL:   1,
		AuthValidTTL:     1,
		OrderTTL:         1,
		FailureWindow:    1,
		FailureThreshold: 1,
		NonceCapacity:    1,
		PendingAuthLimit: 1,
	}

	fields := []func(*Config){
		func(c *Config) { c.AuthPendingTTL = 0 },
		func(c *Config) { c.AuthValidTTL = 1_000_000_001 },
		func(c *Config) { c.OrderTTL = 0 },
		func(c *Config) { c.FailureWindow = 1_000_000_001 },
		func(c *Config) { c.FailureThreshold = 1001 },
		func(c *Config) { c.NonceCapacity = 0 },
		func(c *Config) { c.NonceCapacity = 1_000_001 },
		func(c *Config) { c.PendingAuthLimit = 0 },
		func(c *Config) { c.PendingAuthLimit = 10_001 },
	}

	for _, mutate := range fields {
		config := valid
		mutate(&config)
		_, err := New(config)
		assertErrorKind(t, err, KindInvalidArgument)
	}
}
