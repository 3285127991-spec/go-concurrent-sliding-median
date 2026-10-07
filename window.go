// Package slidingmedian implements a concurrency-safe, fixed-capacity
// sliding window over int64 values with an incremental lower-median query.
package slidingmedian

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

var (
	// ErrEmpty is returned by Median when the window holds no elements.
	ErrEmpty = errors.New("slidingmedian: window is empty")
	// ErrClosed is returned by Add after Close has been called.
	ErrClosed = errors.New("slidingmedian: window is closed")
	// ErrInvalidCapacity is returned by New when capacity <= 0.
	ErrInvalidCapacity = errors.New("slidingmedian: capacity must be > 0")
	// ErrSeqExhausted is returned by Add if the uint64 sequence space is
	// ever exhausted; it cannot happen in practice (2^64-1 successful adds).
	ErrSeqExhausted = errors.New("slidingmedian: sequence space exhausted")
)

// Item is a single window element, uniquely identified by its Seq.
type Item struct {
	Seq   uint64
	Value int64
}

// Snapshot is an independent copy of the window state. Mutating it does not
// affect the Window it was taken from.
type Snapshot struct {
	Items     []Item // window elements sorted by Seq ascending
	Median    int64  // current lower median; meaningful only when HasMedian
	HasMedian bool   // false when the window is empty
	NextSeq   uint64 // sequence number the next successful Add will assign
	Closed    bool
}

// Window is a fixed-capacity sliding window of int64 values.
//
// The lower median is maintained incrementally with two heaps (a max-heap
// for the lower half of the values, a min-heap for the upper half) plus lazy
// deletion of evicted elements, so Add and Median stay O(log n) amortized and
// O(1) instead of re-sorting the whole window on every query. All methods are
// safe for concurrent use.
type Window struct {
	mu       sync.Mutex
	capacity int // immutable after New

	lo      entryHeap // max-heap: lower half of the values
	hi      entryHeap // min-heap: upper half of the values
	validLo int       // live (not lazily deleted) entries in lo
	validHi int       // live (not lazily deleted) entries in hi

	live    map[uint64]int64    // seq -> value for elements in the window
	inHi    map[uint64]bool     // seq -> true when the live entry sits in hi
	pending map[uint64]struct{} // evicted seqs still physically in a heap

	nextSeq uint64
	closed  bool
}

// New creates a Window that keeps at most capacity most recent elements.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	w := &Window{
		capacity: capacity,
		live:     make(map[uint64]int64),
		inHi:     make(map[uint64]bool),
		pending:  make(map[uint64]struct{}),
		nextSeq:  1,
	}
	// container/heap keeps the "smallest" element (per less) on top.
	w.lo.less = func(a, b entry) bool { // max-heap by value
		if a.value != b.value {
			return a.value > b.value
		}
		return a.seq > b.seq
	}
	w.hi.less = func(a, b entry) bool { // min-heap by value
		if a.value != b.value {
			return a.value < b.value
		}
		return a.seq < b.seq
	}
	return w, nil
}

// Add inserts value and returns its sequence number. Sequence numbers start
// at 1, increase strictly and never repeat. When the window is full, the
// element with the oldest sequence number is evicted first. Add returns
// ErrClosed after Close has been called.
func (w *Window) Add(value int64) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, ErrClosed
	}
	if w.nextSeq == 0 {
		return 0, ErrSeqExhausted
	}
	seq := w.nextSeq
	w.nextSeq++

	// Live sequence numbers always form a contiguous range ending at
	// seq-1, so the oldest live element is seq-capacity when full.
	if len(w.live) == w.capacity {
		w.markDeleted(seq - uint64(w.capacity))
	}

	// Insert into the lower half unless that would break the partition
	// max(lo) <= min(hi); the comparison must use a valid (cleaned) top.
	// When lo has no valid elements the value goes to hi instead and
	// rebalance moves the minimum back, which restores the partition.
	w.cleanTop(&w.lo)
	if w.validLo > 0 && value <= w.lo.top().value {
		heap.Push(&w.lo, entry{value: value, seq: seq})
		w.validLo++
	} else {
		heap.Push(&w.hi, entry{value: value, seq: seq})
		w.validHi++
		w.inHi[seq] = true
	}
	w.live[seq] = value

	w.rebalance()
	w.cleanTop(&w.lo)
	w.cleanTop(&w.hi)
	w.maybeCompact()
	return seq, nil
}

// Median returns the lower median of the current window: the middle element
// after sorting by value, or the left of the two middle elements when the
// count is even. It returns ErrEmpty when the window is empty.
func (w *Window) Median() (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.live) == 0 {
		return 0, ErrEmpty
	}
	w.cleanTop(&w.lo)
	return w.lo.top().value, nil
}

// Snapshot returns an independent copy of the current window state.
func (w *Window) Snapshot() Snapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	items := make([]Item, 0, len(w.live))
	for seq, value := range w.live {
		items = append(items, Item{Seq: seq, Value: value})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
	snap := Snapshot{Items: items, NextSeq: w.nextSeq, Closed: w.closed}
	if len(w.live) > 0 {
		w.cleanTop(&w.lo)
		snap.Median = w.lo.top().value
		snap.HasMedian = true
	}
	return snap
}

// Len returns the number of elements currently in the window.
func (w *Window) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.live)
}

// Capacity returns the maximum number of elements the window can hold.
func (w *Window) Capacity() int {
	return w.capacity // immutable after New
}

// Close makes the window reject future Add calls. Median, Snapshot, Len and
// Capacity keep working on the data already in the window. Close is safe to
// call multiple times.
func (w *Window) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

// markDeleted lazily removes a live element: its heap entry stays in place
// until it reaches a heap top, but no longer counts toward the median.
func (w *Window) markDeleted(seq uint64) {
	if w.inHi[seq] {
		w.validHi--
	} else {
		w.validLo--
	}
	w.pending[seq] = struct{}{}
	delete(w.live, seq)
	delete(w.inHi, seq)
}

// rebalance restores validLo == validHi || validLo == validHi+1 by moving
// at most one valid element between the heaps.
func (w *Window) rebalance() {
	switch {
	case w.validLo > w.validHi+1:
		w.cleanTop(&w.lo)
		e := heap.Pop(&w.lo).(entry)
		heap.Push(&w.hi, e)
		w.inHi[e.seq] = true
		w.validLo--
		w.validHi++
	case w.validLo < w.validHi:
		w.cleanTop(&w.hi)
		e := heap.Pop(&w.hi).(entry)
		heap.Push(&w.lo, e)
		w.inHi[e.seq] = false
		w.validLo++
		w.validHi--
	}
}

// cleanTop pops lazily-deleted entries exposed at the top of h.
func (w *Window) cleanTop(h *entryHeap) {
	for h.Len() > 0 {
		seq := h.items[0].seq
		if _, ok := w.pending[seq]; !ok {
			return
		}
		delete(w.pending, seq)
		heap.Pop(h)
	}
}

// maybeCompact rebuilds the heaps without their lazily-deleted entries once
// those outnumber the live ones, keeping memory proportional to capacity.
func (w *Window) maybeCompact() {
	if w.lo.Len()+w.hi.Len() <= 2*(w.validLo+w.validHi)+16 {
		return
	}
	w.lo.items = w.liveOnly(w.lo.items)
	heap.Init(&w.lo)
	w.hi.items = w.liveOnly(w.hi.items)
	heap.Init(&w.hi)
	clear(w.pending)
}

func (w *Window) liveOnly(items []entry) []entry {
	kept := items[:0]
	for _, e := range items {
		if _, deleted := w.pending[e.seq]; !deleted {
			kept = append(kept, e)
		}
	}
	return kept
}
