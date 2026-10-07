package slidingmedian_test

import (
	"errors"
	"math"
	"sort"
	"sync"
	"testing"

	"slidingmedian"
)

func mustWindow(t *testing.T, capacity int) *slidingmedian.Window {
	t.Helper()
	w, err := slidingmedian.NewWindow(capacity)
	if err != nil {
		t.Fatalf("NewWindow(%d): %v", capacity, err)
	}
	return w
}

func mustAdd(t *testing.T, w *slidingmedian.Window, value int64) uint64 {
	t.Helper()
	seq, err := w.Add(value)
	if err != nil {
		t.Fatalf("Add(%d): %v", value, err)
	}
	return seq
}

func mustMedian(t *testing.T, w *slidingmedian.Window) int64 {
	t.Helper()
	median, err := w.Median()
	if err != nil {
		t.Fatalf("Median: %v", err)
	}
	return median
}

func TestNewWindowRejectsNonPositiveCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1, -100} {
		if _, err := slidingmedian.NewWindow(capacity); !errors.Is(err, slidingmedian.ErrInvalidCapacity) {
			t.Fatalf("NewWindow(%d) err = %v, want ErrInvalidCapacity", capacity, err)
		}
	}
}

func TestMedianOddAndEvenCounts(t *testing.T) {
	w := mustWindow(t, 10)
	defer w.Close()

	if _, err := w.Median(); !errors.Is(err, slidingmedian.ErrEmpty) {
		t.Fatalf("Median on empty window err = %v, want ErrEmpty", err)
	}

	// Lower median: middle element for odd counts, left middle for even.
	steps := []struct {
		value int64
		want  int64
	}{
		{5, 5}, // [5]
		{1, 1}, // [1 5]
		{9, 5}, // [1 5 9]
		{2, 2}, // [1 2 5 9]
		{7, 5}, // [1 2 5 7 9]
		{3, 3}, // [1 2 3 5 7 9]
	}
	for i, step := range steps {
		seq := mustAdd(t, w, step.value)
		if want := uint64(i + 1); seq != want {
			t.Fatalf("Add(%d) seq = %d, want %d", step.value, seq, want)
		}
		if got := mustMedian(t, w); got != step.want {
			t.Fatalf("after %d adds Median = %d, want %d", i+1, got, step.want)
		}
	}
}

func TestCapacityEvictsOldest(t *testing.T) {
	w := mustWindow(t, 3)
	defer w.Close()

	mustAdd(t, w, 10)
	mustAdd(t, w, 20)
	mustAdd(t, w, 30)
	if got := mustMedian(t, w); got != 20 {
		t.Fatalf("Median = %d, want 20", got)
	}

	// Window is full: each Add evicts the oldest Seq first.
	if seq := mustAdd(t, w, 40); seq != 4 {
		t.Fatalf("seq = %d, want 4", seq)
	}
	if got := mustMedian(t, w); got != 30 { // [20 30 40]
		t.Fatalf("Median = %d, want 30", got)
	}
	mustAdd(t, w, 50) // [30 40 50]
	if got := mustMedian(t, w); got != 40 {
		t.Fatalf("Median = %d, want 40", got)
	}
	if got := w.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	if got := w.Capacity(); got != 3 {
		t.Fatalf("Capacity = %d, want 3", got)
	}

	snap := w.Snapshot()
	want := []slidingmedian.Item{{Seq: 3, Value: 30}, {Seq: 4, Value: 40}, {Seq: 5, Value: 50}}
	if len(snap.Items) != len(want) {
		t.Fatalf("Snapshot items = %v, want %v", snap.Items, want)
	}
	for i, it := range snap.Items {
		if it != want[i] {
			t.Fatalf("Snapshot items[%d] = %+v, want %+v", i, it, want[i])
		}
	}
}

func TestDuplicateValuesEvictExactSeq(t *testing.T) {
	w := mustWindow(t, 2)
	defer w.Close()

	mustAdd(t, w, 7) // Seq 1
	mustAdd(t, w, 7) // Seq 2
	if got := mustMedian(t, w); got != 7 {
		t.Fatalf("Median = %d, want 7", got)
	}

	// Evicts Seq 1 only; Seq 2's identical value must survive.
	mustAdd(t, w, 1) // Seq 3
	snap := w.Snapshot()
	want := []slidingmedian.Item{{Seq: 2, Value: 7}, {Seq: 3, Value: 1}}
	if len(snap.Items) != len(want) {
		t.Fatalf("Snapshot items = %v, want %v", snap.Items, want)
	}
	for i, it := range snap.Items {
		if it != want[i] {
			t.Fatalf("Snapshot items[%d] = %+v, want %+v", i, it, want[i])
		}
	}
	if got := mustMedian(t, w); got != 1 { // [1 7] -> left middle
		t.Fatalf("Median = %d, want 1", got)
	}

	// Evict the surviving 7 (Seq 2); window becomes two 1s.
	mustAdd(t, w, 1) // Seq 4
	if got := mustMedian(t, w); got != 1 {
		t.Fatalf("Median = %d, want 1", got)
	}
	if got := w.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
}

// TestRebalanceAcrossManyRounds drives many add/evict/rebalance rounds
// with duplicate-heavy deterministic values and checks every step
// against a brute-force reference.
func TestRebalanceAcrossManyRounds(t *testing.T) {
	const capacity = 7
	w := mustWindow(t, capacity)
	defer w.Close()

	seed := uint64(0x9e3779b97f4a7c15)
	next := func() int64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int64(seed>>33) % 50 // small range: forces duplicates
	}

	var window []int64 // brute-force reference, oldest first
	for i := 0; i < 60; i++ {
		value := next()
		seq := mustAdd(t, w, value)
		if want := uint64(i + 1); seq != want {
			t.Fatalf("round %d: seq = %d, want %d", i, seq, want)
		}
		window = append(window, value)
		if len(window) > capacity {
			window = window[1:]
		}

		sorted := append([]int64(nil), window...)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
		if got, want := mustMedian(t, w), sorted[(len(sorted)-1)/2]; got != want {
			t.Fatalf("round %d: Median = %d, want %d (window %v)", i, got, want, window)
		}

		snap := w.Snapshot()
		if len(snap.Items) != len(window) {
			t.Fatalf("round %d: %d snapshot items, want %d", i, len(snap.Items), len(window))
		}
		for j, it := range snap.Items {
			if it.Value != window[j] {
				t.Fatalf("round %d: items[%d] = %+v, want value %d", i, j, it, window[j])
			}
			if want := uint64(i - len(window) + j + 2); it.Seq != want {
				t.Fatalf("round %d: items[%d].Seq = %d, want %d", i, j, it.Seq, want)
			}
		}
		if want := uint64(i + 2); snap.NextSeq != want {
			t.Fatalf("round %d: NextSeq = %d, want %d", i, snap.NextSeq, want)
		}
	}
}

func TestInt64Boundaries(t *testing.T) {
	w := mustWindow(t, 4)
	defer w.Close()

	mustAdd(t, w, math.MaxInt64)
	mustAdd(t, w, math.MinInt64)
	mustAdd(t, w, 0)
	mustAdd(t, w, -1)
	// Sorted: [MinInt64 -1 0 MaxInt64] -> left middle is -1.
	if got := mustMedian(t, w); got != -1 {
		t.Fatalf("Median = %d, want -1", got)
	}

	mustAdd(t, w, math.MinInt64) // evicts MaxInt64
	// Sorted: [MinInt64 MinInt64 -1 0] -> left middle is MinInt64.
	if got := mustMedian(t, w); got != math.MinInt64 {
		t.Fatalf("Median = %d, want %d", got, int64(math.MinInt64))
	}

	mustAdd(t, w, math.MaxInt64) // evicts the first MinInt64
	// Sorted: [MinInt64 -1 0 MaxInt64] -> left middle is -1.
	if got := mustMedian(t, w); got != -1 {
		t.Fatalf("Median = %d, want -1", got)
	}
}

func TestSnapshotIsIndependent(t *testing.T) {
	w := mustWindow(t, 3)
	defer w.Close()

	mustAdd(t, w, 3)
	mustAdd(t, w, 1)
	mustAdd(t, w, 2)

	snap := w.Snapshot()
	if snap.Median != 2 || snap.NextSeq != 4 || snap.Closed {
		t.Fatalf("Snapshot = %+v, want Median=2 NextSeq=4 Closed=false", snap)
	}
	want := []slidingmedian.Item{{Seq: 1, Value: 3}, {Seq: 2, Value: 1}, {Seq: 3, Value: 2}}
	for i, it := range snap.Items {
		if it != want[i] {
			t.Fatalf("items[%d] = %+v, want %+v", i, it, want[i])
		}
	}

	// Mutating the returned snapshot must not affect the window.
	snap.Items[0].Value = 999
	snap.Items = append(snap.Items, slidingmedian.Item{Seq: 99, Value: 99})
	snap.Median = -1

	again := w.Snapshot()
	if len(again.Items) != 3 || again.Items[0].Value != 3 {
		t.Fatalf("window state changed after mutating snapshot: %+v", again.Items)
	}
	if got := mustMedian(t, w); got != 2 {
		t.Fatalf("Median = %d, want 2", got)
	}
}

func TestCloseSemantics(t *testing.T) {
	w := mustWindow(t, 2)

	mustAdd(t, w, 10)
	mustAdd(t, w, 20)

	w.Close()
	w.Close() // idempotent: must not panic or corrupt state

	if _, err := w.Add(30); !errors.Is(err, slidingmedian.ErrClosed) {
		t.Fatalf("Add after Close err = %v, want ErrClosed", err)
	}
	// Failed Add must not consume a Seq.
	if got := w.Snapshot().NextSeq; got != 3 {
		t.Fatalf("NextSeq = %d, want 3", got)
	}

	// Existing data stays readable after Close.
	if got := mustMedian(t, w); got != 10 { // [10 20] -> left middle
		t.Fatalf("Median = %d, want 10", got)
	}
	if got := w.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	if got := w.Capacity(); got != 2 {
		t.Fatalf("Capacity = %d, want 2", got)
	}
	snap := w.Snapshot()
	if !snap.Closed || snap.Median != 10 || len(snap.Items) != 2 {
		t.Fatalf("Snapshot after Close = %+v", snap)
	}
}

// TestConcurrentAccess hammers the window from several goroutines.
// Run with -race to prove the absence of data races.
func TestConcurrentAccess(t *testing.T) {
	const (
		goroutines = 4
		addsEach   = 25
		capacity   = 16
	)
	w := mustWindow(t, capacity)

	var wg sync.WaitGroup
	seqs := make(chan uint64, goroutines*addsEach)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := 0; i < addsEach; i++ {
				seq, err := w.Add(base + int64(i))
				if err != nil {
					t.Errorf("Add: %v", err)
					return
				}
				seqs <- seq
				if _, err := w.Median(); err != nil {
					t.Errorf("Median: %v", err)
					return
				}
				_ = w.Snapshot()
				_ = w.Len()
				_ = w.Capacity()
			}
		}(int64(g * 1000))
	}
	wg.Wait()
	close(seqs)

	seen := make(map[uint64]bool, goroutines*addsEach)
	for seq := range seqs {
		if seq == 0 || seen[seq] {
			t.Fatalf("duplicate or zero Seq %d", seq)
		}
		seen[seq] = true
	}
	if got, want := len(seen), goroutines*addsEach; got != want {
		t.Fatalf("got %d unique Seqs, want %d", got, want)
	}
	if got := w.Len(); got != capacity {
		t.Fatalf("Len = %d, want %d", got, capacity)
	}
	snap := w.Snapshot()
	if len(snap.Items) != capacity {
		t.Fatalf("Snapshot has %d items, want %d", len(snap.Items), capacity)
	}
	for i := 1; i < len(snap.Items); i++ {
		if snap.Items[i].Seq <= snap.Items[i-1].Seq {
			t.Fatalf("Snapshot items not ordered by Seq: %v", snap.Items)
		}
	}
	if _, err := w.Median(); err != nil {
		t.Fatalf("Median after concurrent adds: %v", err)
	}
	w.Close()
	if _, err := w.Add(1); !errors.Is(err, slidingmedian.ErrClosed) {
		t.Fatalf("Add after Close err = %v, want ErrClosed", err)
	}
}
