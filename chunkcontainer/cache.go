package chunkcontainer

import "container/list"

type cacheEntry struct {
	index int
	data  []byte
}

type blockCache struct {
	capacity int
	items    map[int]*list.Element
	order    *list.List
}

func newBlockCache(capacity int) *blockCache {
	return &blockCache{
		capacity: capacity,
		items:    make(map[int]*list.Element),
		order:    list.New(),
	}
}

func (cache *blockCache) peek(index int) ([]byte, bool) {
	if cache.capacity == 0 {
		return nil, false
	}
	element, ok := cache.items[index]
	if !ok {
		return nil, false
	}
	return element.Value.(cacheEntry).data, true
}

func (cache *blockCache) touch(index int, data []byte) {
	if cache.capacity == 0 {
		return
	}
	if element, ok := cache.items[index]; ok {
		element.Value = cacheEntry{index: index, data: data}
		cache.order.MoveToBack(element)
		return
	}

	if cache.order.Len() >= cache.capacity {
		oldest := cache.order.Front()
		entry := oldest.Value.(cacheEntry)
		delete(cache.items, entry.index)
		cache.order.Remove(oldest)
	}

	element := cache.order.PushBack(cacheEntry{index: index, data: data})
	cache.items[index] = element
}

func (cache *blockCache) remove(index int) {
	element, ok := cache.items[index]
	if !ok {
		return
	}
	delete(cache.items, index)
	cache.order.Remove(element)
}
