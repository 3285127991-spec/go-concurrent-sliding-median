// Package slidingmedian implements a concurrency-safe, fixed-capacity
// sliding window over int64 values that maintains the lower median of
// the values it currently holds.
//
// The median is maintained incrementally with two heaps (a max-heap for
// the lower half and a min-heap for the upper half) plus a lazy-deletion
// mechanism for evicted elements, so Add, eviction and rebalancing all
// cost O(log n) and Median is O(1) amortized. The window never re-sorts
// its contents. It is pure in-memory: no persistence, no network, no
// background goroutines.
package slidingmedian

import (
	"container/heap"
	"errors"
	"sync"
)

var (
	// ErrEmpty is returned by Median when the window holds no elements.
	ErrEmpty = errors.New("slidingmedian: window is empty")
	// ErrClosed is returned by Add after Close has been called.
	ErrClosed = errors.New("slidingmedian: window is closed")
	// ErrInvalidCapacity is returned by NewWindow when capacity <= 0.
	ErrInvalidCapacity = errors.New("slidingmedian: capacity must be > 0")
)

// Item is a single window element as reported by Snapshot.
type Item struct {
	Seq   uint64
	Value int64
}

// Snapshot is a point-in-time, independent copy of the window state.
// Mutating it never affects the window.
type Snapshot struct {
	Items   []Item // live elements ordered by ascending Seq
	Median  int64  // lower median of Items; zero when the window is empty
	NextSeq uint64 // Seq the next successful Add will assign
	Closed  bool   // whether Close has been called
}

// Heap identifiers recorded in Window.where for each live Seq.
const (
	heapLo = iota // lower half (max-heap)
	heapHi        // upper half (min-heap)
)

// Window is a fixed-capacity sliding window over int64 values.
//
// It retains the most recent Capacity() values added; once full, every
// Add first evicts the oldest element (lowest Seq). The lower median of
// the retained values is available through Median. All methods are safe
// for concurrent use by multiple goroutines.
type Window struct {
	mu       sync.Mutex
	capacity int
	lo       maxHeap          // lower half; lo[0] (after pruning) is the median
	hi       minHeap          // upper half
	where    map[uint64]int   // Seq -> heap physically holding the live element
	vals     map[uint64]int64 // Seq -> value for every live element
	queue    []uint64         // live Seqs in insertion (FIFO) order
	delayed  map[uint64]bool  // evicted Seqs not yet popped from their heap
	loSize   int              // live (non-delayed) elements in lo
	hiSize   int              // live (non-delayed) elements in hi
	nextSeq  uint64           // next Seq to assign; starts at 1
	closed   bool
}

// NewWindow returns a Window retaining at most capacity values.
// It returns ErrInvalidCapacity when capacity <= 0.
func NewWindow(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Window{
		capacity: capacity,
		where:    make(map[uint64]int),
		vals:     make(map[uint64]int64),
		delayed:  make(map[uint64]bool),
		nextSeq:  1,
	}, nil
}

// Add inserts value and returns its assigned Seq. Seqs start at 1 and
// are strictly increasing across successful calls; a failed Add never
// consumes a Seq. When the window is full, the oldest element (lowest
// Seq) is evicted first. Add returns ErrClosed once Close was called.
func (w *Window) Add(value int64) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrClosed
	}
	if len(w.vals) == w.capacity {
		w.evictOldest()
	}

	seq := w.nextSeq
	w.nextSeq++

	w.prune() // ensure lo[0] is live before comparing against it
	if w.loSize == 0 || value <= w.lo[0].value {
		heap.Push(&w.lo, heapItem{value: value, seq: seq})
		w.loSize++
		w.where[seq] = heapLo
	} else {
		heap.Push(&w.hi, heapItem{value: value, seq: seq})
		w.hiSize++
		w.where[seq] = heapHi
	}
	w.vals[seq] = value
	w.queue = append(w.queue, seq)
	w.rebalance()
	return seq, nil
}

// Median returns the lower median of the values currently in the
// window: the middle element for an odd count, the left of the two
// middle elements for an even count (e.g. [1,2,3,4] -> 2). It returns
// ErrEmpty when the window is empty.
func (w *Window) Median() (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.vals) == 0 {
		return 0, ErrEmpty
	}
	w.prune()
	return w.lo[0].value, nil
}

// Snapshot returns an independent copy of the current window state:
// the live elements ordered by ascending Seq, the current Median, the
// NextSeq that the next successful Add will assign, and whether the
// window is Closed. Mutating the result never affects the window.
func (w *Window) Snapshot() Snapshot {
	w.mu.Lock()
	defer w.mu.Unlock()

	items := make([]Item, 0, len(w.vals))
	for _, seq := range w.queue {
		items = append(items, Item{Seq: seq, Value: w.vals[seq]})
	}
	snap := Snapshot{
		Items:   items,
		NextSeq: w.nextSeq,
		Closed:  w.closed,
	}
	if len(items) > 0 {
		w.prune()
		snap.Median = w.lo[0].value
	}
	return snap
}

// Len reports the number of elements currently held by the window.
func (w *Window) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.vals)
}

// Capacity reports the maximum number of elements the window retains.
func (w *Window) Capacity() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.capacity
}

// Close makes the window reject further Add calls with ErrClosed.
// Elements already in the window remain readable through Median,
// Snapshot, Len and Capacity. Close is idempotent.
func (w *Window) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

// evictOldest removes the oldest live element from the window. Its heap
// entry is not removed physically; it is marked in delayed and popped
// lazily once it reaches a heap top. Caller must hold w.mu.
func (w *Window) evictOldest() {
	seq := w.queue[0]
	w.queue = w.queue[1:]
	delete(w.vals, seq)
	if w.where[seq] == heapLo {
		w.loSize--
	} else {
		w.hiSize--
	}
	delete(w.where, seq)
	w.delayed[seq] = true
}

// rebalance restores the invariant loSize == hiSize or
// loSize == hiSize+1 by moving live heap-top elements between heaps.
// Caller must hold w.mu.
func (w *Window) rebalance() {
	for w.loSize > w.hiSize+1 {
		w.prune()
		it := heap.Pop(&w.lo).(heapItem)
		w.loSize--
		heap.Push(&w.hi, it)
		w.hiSize++
		w.where[it.seq] = heapHi
	}
	for w.loSize < w.hiSize {
		w.prune()
		it := heap.Pop(&w.hi).(heapItem)
		w.hiSize--
		heap.Push(&w.lo, it)
		w.loSize++
		w.where[it.seq] = heapLo
	}
}

// prune physically pops heap-top entries whose Seq was already evicted
// (lazy deletion), keeping heap tops live and heap sizes consistent.
// Caller must hold w.mu.
func (w *Window) prune() {
	for len(w.lo) > 0 && w.delayed[w.lo[0].seq] {
		delete(w.delayed, w.lo[0].seq)
		heap.Pop(&w.lo)
	}
	for len(w.hi) > 0 && w.delayed[w.hi[0].seq] {
		delete(w.delayed, w.hi[0].seq)
		heap.Pop(&w.hi)
	}
}
