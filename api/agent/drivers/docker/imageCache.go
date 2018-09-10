/*
ImageCache holds all the logic for calculating what docker images can be removed from the running agent.
The last used time and the number of uses are both taken into account to calculate a score (timeSinceLastUse/uses)
The higher the score the more evicitable the image is.

ImageCache also provides a method to "lock" an image, insuring it is never deleted. To do so a Lock is called with
the image ID to lock, as well as a token. The token is then added to a set of tokens attached to that entry.
The set is a map of *interface -> *interface where both values are the same.
*/

package docker

import (
	"errors"
	"sort"
	"sync"
	"time"

	d "github.com/fsouza/go-dockerclient"
	"github.com/sirupsen/logrus"
)

// Cache is an LRU cache, safe for concurrent access.
type Cache struct {
	totalSize int64
	mu        sync.Mutex
	cache     EntryByAge
	maxSize   int64
}

type Entry struct {
	lastUsed time.Time
	locked   map[*interface{}]*interface{}
	uses     int64
	image    d.APIImages
}

func (e Entry) Score() int64 {
	age := time.Now().Sub(e.lastUsed)
	return age.Nanoseconds() / e.uses
}

type EntryByAge []Entry

func (a EntryByAge) Len() int           { return len(a) }
func (a EntryByAge) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a EntryByAge) Less(i, j int) bool { return a[i].Score() < a[j].Score() }

func NewEntry(value d.APIImages) Entry {
	return Entry{
		lastUsed: time.Now(),
		locked:   make(map[*interface{}]*interface{}),
		uses:     0,
		image:    value}
}

// New returns a new cache with the provided maximum items.
func NewCache(maxSize int64) *Cache {
	return &Cache{
		cache: make(EntryByAge, 0),
		mu:    sync.Mutex{},
	}
}

func (c *Cache) Contains(value d.APIImages) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.contains(value)
}

func (c *Cache) contains(value d.APIImages) bool {
	for _, i := range c.cache {
		if i.image.ID == value.ID {
			return true
		}
	}
	return false

}
func (c *Cache) Mark(ID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mark(ID)
}

func (c *Cache) mark(ID string) error {
	for idx, i := range c.cache {
		if i.image.ID == ID {
			c.cache[idx].lastUsed = time.Now()
			c.cache[idx].uses = c.cache[idx].uses + 1
			return nil
		}
	}

	return errors.New("Image not found in cache")
}

func (c *Cache) Remove(value d.APIImages) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for idx, i := range c.cache {
		if i.image.ID == value.ID {
			// Move the last item into the location of the item to be removed
			c.cache[idx] = c.cache[len(c.cache)-1]
			// shorten the list
			c.cache = c.cache[:len(c.cache)-1]
			return nil
		}
	}

	return errors.New("Image not found in cache")
}

func (c *Cache) Lock(ID string, key interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lock(ID, key)
}

func (c *Cache) lock(ID string, key interface{}) error {
	for _, i := range c.cache {
		if i.image.ID == ID {
			i.locked[&key] = &key
			return nil
		}
	}
	return errors.New("Image not found in cache")
}

func (c *Cache) Locked(ID string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.locked(ID)
}

func (c *Cache) locked(ID string) (bool, error) {
	for _, i := range c.cache {
		if i.image.ID == ID {
			return len(i.locked) > 0, nil
		}
	}
	return false, errors.New("Image not found in cache")
}

func (c *Cache) Unlock(ID string, key interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unlock(ID, key)
}

func (c *Cache) unlock(ID string, key interface{}) {
	for _, i := range c.cache {
		if i.image.ID == ID {
			delete(i.locked, &key)
		}
	}
}

// Add adds the provided key and value to the cache, evicting
// an old item if necessary.
func (c *Cache) Add(value d.APIImages) {
	c.mu.Lock()
	defer c.mu.Unlock()
	logrus.Debugf("value: %v", value)
	if c.contains(value) {
		c.mark(value.ID)
		return
	}
	c.cache = append(c.cache, NewEntry(value))
}

func (c *Cache) TotalSize() int64 {
	return 0
}

func (c *Cache) OverFilled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.TotalSize() > c.maxSize
}

func (c *Cache) Evictable() EntryByAge {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictable()
}

func (c *Cache) evictable() (ea EntryByAge) {
	for _, i := range c.cache {
		if len(i.locked) == 0 {
			ea = append(ea, i)
		}
	}
	sort.Sort(ea)
	return ea
}

// Len returns the number of items in the cache.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cache)
}
