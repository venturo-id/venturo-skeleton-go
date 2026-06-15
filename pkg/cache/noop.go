package cache

import (
	"context"
	"encoding/json"
	"time"
)

// NoOpCache satisfies Cache but never stores anything. Useful in tests or when
// caching should be disabled. GetOrSet still invokes the loader (and returns
// its value) so callers behave correctly; it just doesn't persist.
type NoOpCache struct{}

func (NoOpCache) Get(context.Context, string, any) (bool, error) { return false, nil }

func (NoOpCache) Set(context.Context, string, any, time.Duration) error { return nil }

func (NoOpCache) Delete(context.Context, ...string) error { return nil }

func (NoOpCache) GetOrSet(_ context.Context, _ string, dest any, _ time.Duration, loader func() (any, error)) error {
	val, err := loader()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dest)
}
