// Package rootcache is a per-repository-root cache whose loader runs at most
// once per root until the root is dropped (project configuration, manifests).
package rootcache

import "sync"

// Cache caches one T per root. The zero value is ready to use.
type Cache[T any] struct {
	mu sync.Mutex
	m  map[string]*entry[T]
}

type entry[T any] struct {
	once sync.Once
	v    T
}

// Get returns root's value, loading it on first use; concurrent callers for
// the same root wait for one load.
func (c *Cache[T]) Get(root string, load func(root string) T) T {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]*entry[T]{}
	}
	e, ok := c.m[root]
	if !ok {
		e = &entry[T]{}
		c.m[root] = e
	}
	c.mu.Unlock()
	e.once.Do(func() { e.v = load(root) })
	return e.v
}

// Drop removes root and returns the value that was cached (none when root
// was not cached). The next Get loads again.
func (c *Cache[T]) Drop(root string) []T {
	c.mu.Lock()
	var es []*entry[T]
	if e, ok := c.m[root]; ok {
		es = append(es, e)
		delete(c.m, root)
	}
	c.mu.Unlock()
	return values(es)
}

// DropAll removes every root and returns the values that were cached.
func (c *Cache[T]) DropAll() []T {
	c.mu.Lock()
	es := make([]*entry[T], 0, len(c.m))
	for _, e := range c.m {
		es = append(es, e)
	}
	c.m = nil
	c.mu.Unlock()
	return values(es)
}

func values[T any](es []*entry[T]) []T {
	out := make([]T, 0, len(es))
	for _, e := range es {
		e.once.Do(func() {}) // wait for a concurrent load before reading v
		out = append(out, e.v)
	}
	return out
}
