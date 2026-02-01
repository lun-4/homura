package main

import (
	"sync"
	"time"

	"github.com/hugelgupf/p9/p9"
)

// AttrCache caches file attributes to reduce 9p round-trips
type AttrCache struct {
	mu      sync.RWMutex
	entries map[string]*cachedAttr
	ttl     time.Duration
}

type cachedAttr struct {
	qid    p9.QID
	attr   p9.Attr
	expiry time.Time
}

// NewAttrCache creates a new attribute cache with the given TTL
func NewAttrCache(ttl time.Duration) *AttrCache {
	return &AttrCache{
		entries: make(map[string]*cachedAttr),
		ttl:     ttl,
	}
}

// Get retrieves cached attributes for a path
func (c *AttrCache) Get(path string) (p9.QID, p9.Attr, bool) {
	c.mu.RLock()
	entry, ok := c.entries[path]
	c.mu.RUnlock()

	if !ok {
		return p9.QID{}, p9.Attr{}, false
	}

	if time.Now().After(entry.expiry) {
		// Expired - remove and return miss
		c.mu.Lock()
		delete(c.entries, path)
		c.mu.Unlock()
		return p9.QID{}, p9.Attr{}, false
	}

	return entry.qid, entry.attr, true
}

// Put stores attributes for a path
func (c *AttrCache) Put(path string, qid p9.QID, attr p9.Attr) {
	c.mu.Lock()
	c.entries[path] = &cachedAttr{
		qid:    qid,
		attr:   attr,
		expiry: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// PutBatch stores multiple entries at once (more efficient)
func (c *AttrCache) PutBatch(attrs map[string]struct {
	QID  p9.QID
	Attr p9.Attr
}) {
	expiry := time.Now().Add(c.ttl)
	c.mu.Lock()
	for path, a := range attrs {
		c.entries[path] = &cachedAttr{
			qid:    a.QID,
			attr:   a.Attr,
			expiry: expiry,
		}
	}
	c.mu.Unlock()
}

// Invalidate removes a path from the cache
func (c *AttrCache) Invalidate(path string) {
	c.mu.Lock()
	delete(c.entries, path)
	c.mu.Unlock()
}

// InvalidatePrefix removes all paths with the given prefix
func (c *AttrCache) InvalidatePrefix(prefix string) {
	c.mu.Lock()
	for path := range c.entries {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			delete(c.entries, path)
		}
	}
	c.mu.Unlock()
}

// Clear removes all entries
func (c *AttrCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]*cachedAttr)
	c.mu.Unlock()
}

// Size returns the number of cached entries
func (c *AttrCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Cleanup removes expired entries (call periodically)
func (c *AttrCache) Cleanup() {
	now := time.Now()
	c.mu.Lock()
	for path, entry := range c.entries {
		if now.After(entry.expiry) {
			delete(c.entries, path)
		}
	}
	c.mu.Unlock()
}
