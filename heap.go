package slidingmedian

// entry is one heap element: a value plus the sequence number that
// identifies it for lazy deletion.
type entry struct {
	value int64
	seq   uint64
}

// entryHeap adapts a slice of entries to container/heap. The less function
// decides the ordering, so the same type serves as the max-heap (lower half)
// and the min-heap (upper half).
type entryHeap struct {
	items []entry
	less  func(a, b entry) bool
}

func (h entryHeap) Len() int           { return len(h.items) }
func (h entryHeap) Less(i, j int) bool { return h.less(h.items[i], h.items[j]) }
func (h entryHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *entryHeap) Push(x any) { h.items = append(h.items, x.(entry)) }

func (h *entryHeap) Pop() any {
	old := h.items
	n := len(old)
	e := old[n-1]
	old[n-1] = entry{}
	h.items = old[:n-1]
	return e
}

// top returns the root entry; the caller must ensure the heap is non-empty.
func (h *entryHeap) top() entry { return h.items[0] }
