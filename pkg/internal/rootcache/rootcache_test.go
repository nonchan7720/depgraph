package rootcache

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLoadsOncePerRoot(t *testing.T) {
	var c Cache[string]
	var loads atomic.Int32
	load := func(root string) string { loads.Add(1); return "v:" + root }
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if got := c.Get("/a", load); got != "v:/a" {
				t.Errorf("Get = %q", got)
			}
		})
	}
	wg.Wait()
	if n := loads.Load(); n != 1 {
		t.Errorf("loaded %d times, want 1", n)
	}
	c.Get("/b", load)
	if n := loads.Load(); n != 2 {
		t.Errorf("another root: %d loads", n)
	}
}

func TestDrop(t *testing.T) {
	var c Cache[int]
	n := 0
	load := func(string) int { n++; return n }
	c.Get("/a", load)
	if got := c.Drop("/a"); !slices.Equal(got, []int{1}) {
		t.Errorf("Drop = %v", got)
	}
	if got := c.Drop("/missing"); len(got) != 0 {
		t.Errorf("Drop of an unknown root = %v", got)
	}
	if v := c.Get("/a", load); v != 2 {
		t.Errorf("after Drop the next Get loads again: %d", v)
	}
	c.Get("/b", load)
	got := c.DropAll()
	slices.Sort(got)
	if !slices.Equal(got, []int{2, 3}) {
		t.Errorf("DropAll = %v", got)
	}
	var zero Cache[int]
	if got := zero.Drop("/x"); len(got) != 0 {
		t.Error("zero value is usable")
	}
}
