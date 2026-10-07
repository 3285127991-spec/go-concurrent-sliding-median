package slidingmedian

// heapItem is one element stored in the median heaps. The Seq tag makes
// elements with equal values distinct, so lazy deletion never removes
// the wrong duplicate.
type heapItem struct {
	value int64
	seq   uint64
}

// minHeap orders by (value, seq) ascending and implements heap.Interface.
type minHeap []heapItem

func (h minHeap) Len() int { return len(h) }

func (h minHeap) Less(i, j int) bool {
	if h[i].value != h[j].value {
		return h[i].value < h[j].value
	}
	return h[i].seq < h[j].seq
}

func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *minHeap) Push(x any) { *h = append(*h, x.(heapItem)) }

func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = heapItem{}
	*h = old[:n-1]
	return it
}

// maxHeap orders by (value, seq) descending and implements heap.Interface.
type maxHeap []heapItem

func (h maxHeap) Len() int { return len(h) }

func (h maxHeap) Less(i, j int) bool {
	if h[i].value != h[j].value {
		return h[i].value > h[j].value
	}
	return h[i].seq > h[j].seq
}

func (h maxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *maxHeap) Push(x any) { *h = append(*h, x.(heapItem)) }

func (h *maxHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = heapItem{}
	*h = old[:n-1]
	return it
}
