// Command slidingmedian is a small demo of the slidingmedian library:
// step-by-step adds, odd/even lower medians, capacity-driven rolling
// eviction, duplicate values, and reads after Close.
package main

import (
	"errors"
	"fmt"

	"slidingmedian"
)

func add(w *slidingmedian.Window, value int64) {
	seq, err := w.Add(value)
	if err != nil {
		fmt.Printf("Add(%d) -> error: %v\n", value, err)
		return
	}
	median, _ := w.Median()
	fmt.Printf("Add(%2d) -> Seq=%d  window=%v  len=%d  median=%d\n",
		value, seq, valuesOf(w.Snapshot()), w.Len(), median)
}

func valuesOf(snap slidingmedian.Snapshot) []int64 {
	values := make([]int64, len(snap.Items))
	for i, it := range snap.Items {
		values[i] = it.Value
	}
	return values
}

func main() {
	w, err := slidingmedian.NewWindow(3)
	if err != nil {
		panic(err)
	}
	fmt.Printf("new sliding window: capacity=%d (lower median)\n\n", w.Capacity())

	fmt.Println("== step-by-step Add: odd/even lower median ==")
	add(w, 4) // [4]     odd  -> 4
	add(w, 2) // [4 2]   even -> 2 (left of the two middles)
	add(w, 9) // [4 2 9] odd  -> 4

	fmt.Println("\n== capacity=3 rolling eviction: oldest Seq evicted ==")
	add(w, 7) // evicts Seq 1 (4)
	add(w, 1) // evicts Seq 2 (2)

	fmt.Println("\n== duplicate values: eviction removes exactly one Seq ==")
	add(w, 9) // evicts Seq 3 (9); the other 9s stay
	add(w, 9) // evicts Seq 4 (7)

	fmt.Println("\n== Close: Add rejected, existing data still readable ==")
	w.Close()
	w.Close() // idempotent
	if _, err := w.Add(100); errors.Is(err, slidingmedian.ErrClosed) {
		fmt.Println("Add(100) -> ErrClosed (as expected)")
	}
	median, _ := w.Median()
	fmt.Printf("after Close: median=%d len=%d capacity=%d\n",
		median, w.Len(), w.Capacity())

	snap := w.Snapshot()
	fmt.Printf("snapshot: median=%d nextSeq=%d closed=%v items=%v\n",
		snap.Median, snap.NextSeq, snap.Closed, snap.Items)
}
