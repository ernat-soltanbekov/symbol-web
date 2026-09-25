package ai

import (
	"sync"
	"time"
)

const (
	cacheTTL      = 5 * time.Minute
	cacheCapacity = 128
)

type cachedSuggestions struct {
	values  []string
	expires time.Time
}

// suggestionCache is lazy-initialized, bounded and safe for concurrent requests.
// Values are copied in both directions so callers cannot mutate cached responses.
type suggestionCache struct {
	mu      sync.Mutex
	entries map[string]cachedSuggestions
}

func (c *suggestionCache) get(key string, now time.Time) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !now.Before(entry.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return append([]string(nil), entry.values...), true
}

func (c *suggestionCache) put(key string, values []string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]cachedSuggestions)
	}
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, key)
		}
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= cacheCapacity {
		var oldestKey string
		var oldestTime time.Time
		for key, entry := range c.entries {
			if oldestTime.IsZero() || entry.expires.Before(oldestTime) {
				oldestKey, oldestTime = key, entry.expires
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = cachedSuggestions{values: append([]string(nil), values...), expires: now.Add(cacheTTL)}
}
