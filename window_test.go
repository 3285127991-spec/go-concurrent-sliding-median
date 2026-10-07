package slidingmedian_test

import (
	"cmp"
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"slidingmedian"
)

// refModel is a naive reference implementation used to cross-check Window.
type refModel struct {
	cap  int
	vals []int64 // last cap values in arrival order
}

func (m *refModel) add(v int64) {
	m.vals = append(m.vals, v)
	if len(m.vals) > m.cap {
		m.vals = m.vals[1:]
	}
}

func (m *refModel) median() int64 {
	s := slices.Clone(m.vals)
	slices.Sort(s)
	return s[(len(s)-1)/2]
}

func mustAdd(t *testing.T, w *slidingmedian.Window, v int64) uint64 {
	t.Helper()
	seq, err := w.Add(v)
	if err != nil {
		t.Fatalf("Add(%d): %v", v, err)
	}
	return seq
}

func mustMedian(t *testing.T, w *slidingmedian.Window) int64 {
	t.Helper()
	m, err := w.Median()
	if err != nil {
		t.Fatalf("Median: %v", err)
	}
	return m
}

func TestMedianOddAndEven(t *testing.T) {
	w, err := slidingmedian.New(10)
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		add  int64
		want int64
	}{
		{5, 5},   // [5]
		{1, 1},   // [1 5] -> left of the two middles
		{3, 3},   // [1 3 5]
		{2, 2},   // [1 2 3 5] -> left of the two middles
		{100, 3}, // [1 2 3 5 100]
		{4, 3},   // [1 2 3 4 5 100] -> left of the two middles
	}
	for _, s := range steps {
		mustAdd(t, w, s.add)
		if got := mustMedian(t, w); got != s.want {
			t.Fatalf("after Add(%d): Median=%d, want %d", s.add, got, s.want)
		}
	}
}

func TestEmptyWindow(t *testing.T) {
	w, err := slidingmedian.New(3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Median(); !errors.Is(err, slidingmedian.ErrEmpty) {
		t.Fatalf("Median on empty window: %v, want ErrEmpty", err)
	}
	if got := w.Len(); got != 0 {
		t.Fatalf("Len=%d, want 0", got)
	}
	if got := w.Capacity(); got != 3 {
		t.Fatalf("Capacity=%d, want 3", got)
	}
	snap := w.Snapshot()
	if len(snap.Items) != 0 || snap.HasMedian || snap.NextSeq != 1 || snap.Closed {
		t.Fatalf("unexpected empty snapshot: %+v", snap)
	}
}

func TestCapacityRollingEviction(t *testing.T) {
	w, err := slidingmedian.New(3)
	if err != nil {
		t.Fatal(err)
	}
	ref := &refModel{cap: 3}
	for v := int64(1); v <= 8; v++ {
		if seq := mustAdd(t, w, v); seq != uint64(v) {
			t.Fatalf("Add(%d) seq=%d, want %d", v, seq, v)
		}
		ref.add(v)
		if got := w.Len(); got != min(int(v), 3) {
			t.Fatalf("after Add(%d): Len=%d, want %d", v, got, min(int(v), 3))
		}
		if got := mustMedian(t, w); got != ref.median() {
			t.Fatalf("after Add(%d): Median=%d, want %d", v, got, ref.median())
		}
		snap := w.Snapshot()
		wantOldest := uint64(1)
		if v > 3 {
			wantOldest = uint64(v - 2)
		}
		if snap.Items[0].Seq != wantOldest {
			t.Fatalf("after Add(%d): oldest seq=%d, want %d", v, snap.Items[0].Seq, wantOldest)
		}
		if snap.Items[len(snap.Items)-1].Seq != uint64(v) {
			t.Fatalf("after Add(%d): newest seq=%d, want %d", v, snap.Items[len(snap.Items)-1].Seq, v)
		}
	}
}

func TestDuplicateValuesPreciseEviction(t *testing.T) {
	w, err := slidingmedian.New(3)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, w, 7) // seq 1
	mustAdd(t, w, 7) // seq 2
	mustAdd(t, w, 7) // seq 3
	mustAdd(t, w, 9) // seq 4, evicts seq 1 only
	snap := w.Snapshot()
	want := []slidingmedian.Item{{Seq: 2, Value: 7}, {Seq: 3, Value: 7}, {Seq: 4, Value: 9}}
	if !slices.Equal(snap.Items, want) {
		t.Fatalf("snapshot=%v, want %v", snap.Items, want)
	}
	if got := mustMedian(t, w); got != 7 { // [7 7 9]
		t.Fatalf("Median=%d, want 7", got)
	}
	mustAdd(t, w, 9) // seq 5, evicts seq 2 only
	snap = w.Snapshot()
	want = []slidingmedian.Item{{Seq: 3, Value: 7}, {Seq: 4, Value: 9}, {Seq: 5, Value: 9}}
	if !slices.Equal(snap.Items, want) {
		t.Fatalf("snapshot=%v, want %v", snap.Items, want)
	}
	if got := mustMedian(t, w); got != 9 { // [7 9 9]
		t.Fatalf("Median=%d, want 9", got)
	}
}
func TestRebalanceAcrossPatterns(t *testing.T) {
	const n = 40
	asc := make([]int64, n)
	desc := make([]int64, n)
	alt := make([]int64, n)
	for i := range asc {
		asc[i] = int64(i)
		desc[i] = int64(n - i)
		if i%2 == 0 {
			alt[i] = -100
		} else {
			alt[i] = 100
		}
	}
	rng := rand.New(rand.NewSource(1))
	random := make([]int64, 200)
	for i := range random {
		random[i] = int64(rng.Intn(41) - 20) // small range: forces duplicates
	}
	patterns := []struct {
		name string
		vals []int64
	}{
		{"ascending", asc},
		{"descending", desc},
		{"alternating", alt},
		{"random", random},
	}
	for _, p := range patterns {
		for _, capacity := range []int{1, 2, 7, 8} {
			w, err := slidingmedian.New(capacity)
			if err != nil {
				t.Fatal(err)
			}
			ref := &refModel{cap: capacity}
			for step, v := range p.vals {
				mustAdd(t, w, v)
				ref.add(v)
				if got := mustMedian(t, w); got != ref.median() {
					t.Fatalf("%s cap=%d step=%d: Median=%d, want %d",
						p.name, capacity, step, got, ref.median())
				}
				snap := w.Snapshot()
				if len(snap.Items) != len(ref.vals) {
					t.Fatalf("%s cap=%d step=%d: %d items, want %d",
						p.name, capacity, step, len(snap.Items), len(ref.vals))
				}
				for j, it := range snap.Items {
					if it.Value != ref.vals[j] {
						t.Fatalf("%s cap=%d step=%d: items[%d]=%+v, want value %d",
							p.name, capacity, step, j, it, ref.vals[j])
					}
				}
			}
		}
	}
}

func TestInt64Boundaries(t *testing.T) {
	w, err := slidingmedian.New(5)
	if err != nil {
		t.Fatal(err)
	}
	ref := &refModel{cap: 5}
	vals := []int64{
		math.MaxInt64, math.MinInt64, 0, -1, 1,
		math.MinInt64, math.MaxInt64, math.MaxInt64 - 1, math.MinInt64 + 1, 0,
	}
	for i, v := range vals {
		mustAdd(t, w, v)
		ref.add(v)
		if got := mustMedian(t, w); got != ref.median() {
			t.Fatalf("step %d (v=%d): Median=%d, want %d", i, v, got, ref.median())
		}
	}
}

func TestSnapshotIndependence(t *testing.T) {
	w, err := slidingmedian.New(4)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []int64{4, 1, 3, 2} {
		mustAdd(t, w, v)
	}
	snap := w.Snapshot()
	want := []slidingmedian.Item{
		{Seq: 1, Value: 4}, {Seq: 2, Value: 1}, {Seq: 3, Value: 3}, {Seq: 4, Value: 2},
	}
	if !slices.IsSortedFunc(snap.Items, func(a, b slidingmedian.Item) int {
		return cmp.Compare(a.Seq, b.Seq)
	}) {
		t.Fatalf("snapshot items not sorted by Seq: %v", snap.Items)
	}
	if !slices.Equal(snap.Items, want) {
		t.Fatalf("snapshot=%v, want %v", snap.Items, want)
	}
	if snap.Median != 2 || !snap.HasMedian || snap.NextSeq != 5 || snap.Closed {
		t.Fatalf("unexpected snapshot fields: %+v", snap)
	}

	// Mutating the returned snapshot must not affect the window.
	for i := range snap.Items {
		snap.Items[i] = slidingmedian.Item{Seq: 999, Value: -999}
	}
	snap.Items = append(snap.Items, slidingmedian.Item{Seq: 1000, Value: 1000})
	snap.Median = -999
	snap.NextSeq = 999

	snap2 := w.Snapshot()
	if !slices.Equal(snap2.Items, want) {
		t.Fatalf("after mutating first snapshot: items=%v, want %v", snap2.Items, want)
	}
	if got := mustMedian(t, w); got != 2 {
		t.Fatalf("after mutating first snapshot: Median=%d, want 2", got)
	}
	if snap2.NextSeq != 5 {
		t.Fatalf("after mutating first snapshot: NextSeq=%d, want 5", snap2.NextSeq)
	}
}

func TestClose(t *testing.T) {
	if _, err := slidingmedian.New(0); !errors.Is(err, slidingmedian.ErrInvalidCapacity) {
		t.Fatalf("New(0): %v, want ErrInvalidCapacity", err)
	}
	if _, err := slidingmedian.New(-3); !errors.Is(err, slidingmedian.ErrInvalidCapacity) {
		t.Fatalf("New(-3): %v, want ErrInvalidCapacity", err)
	}

	w, err := slidingmedian.New(2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, w, 10)
	mustAdd(t, w, 20)
	w.Close()
	w.Close() // idempotent

	if _, err := w.Add(30); !errors.Is(err, slidingmedian.ErrClosed) {
		t.Fatalf("Add after Close: %v, want ErrClosed", err)
	}
	if got := mustMedian(t, w); got != 10 { // [10 20] lower median
		t.Fatalf("Median after Close=%d, want 10", got)
	}
	if got := w.Len(); got != 2 {
		t.Fatalf("Len after Close=%d, want 2", got)
	}
	if got := w.Capacity(); got != 2 {
		t.Fatalf("Capacity after Close=%d, want 2", got)
	}
	snap := w.Snapshot()
	if !snap.Closed || snap.NextSeq != 3 || len(snap.Items) != 2 || snap.Median != 10 {
		t.Fatalf("unexpected snapshot after Close: %+v", snap)
	}
}

func TestConcurrentAccess(t *testing.T) {
	const (
		writers       = 8
		addsPerWriter = 50
		capacity      = 64
	)
	w, err := slidingmedian.New(capacity)
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg    sync.WaitGroup
		done  atomic.Bool
		mu    sync.Mutex
		bySeq = make(map[uint64]int64, writers*addsPerWriter)
	)
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := int64(0); i < addsPerWriter; i++ {
				v := base + i
				seq, err := w.Add(v)
				if err != nil {
					t.Errorf("Add(%d): %v", v, err)
					return
				}
				mu.Lock()
				bySeq[seq] = v
				mu.Unlock()
			}
		}(int64(g * 1000))
	}
	var rwg sync.WaitGroup
	for g := 0; g < 2; g++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for !done.Load() {
				_, _ = w.Median()
				_ = w.Snapshot()
				_ = w.Len()
				_ = w.Capacity()
			}
		}()
	}
	wg.Wait()
	done.Store(true)
	rwg.Wait()

	const total = writers * addsPerWriter
	if len(bySeq) != total {
		t.Fatalf("got %d distinct seqs, want %d (duplicate or lost seq)", len(bySeq), total)
	}
	for s := uint64(1); s <= total; s++ {
		if _, ok := bySeq[s]; !ok {
			t.Fatalf("missing seq %d", s)
		}
	}
	if got := w.Len(); got != capacity {
		t.Fatalf("Len=%d, want %d", got, capacity)
	}

	// The final window must hold exactly the last `capacity` seqs.
	type pair struct {
		seq uint64
		val int64
	}
	all := make([]pair, 0, len(bySeq))
	for s, v := range bySeq {
		all = append(all, pair{s, v})
	}
	slices.SortFunc(all, func(a, b pair) int { return cmp.Compare(a.seq, b.seq) })
	tail := all[len(all)-capacity:]
	vals := make([]int64, 0, capacity)
	for _, p := range tail {
		vals = append(vals, p.val)
	}
	slices.Sort(vals)
	wantMedian := vals[(len(vals)-1)/2]
	if got := mustMedian(t, w); got != wantMedian {
		t.Fatalf("Median=%d, want %d", got, wantMedian)
	}
	snap := w.Snapshot()
	if len(snap.Items) != capacity {
		t.Fatalf("snapshot has %d items, want %d", len(snap.Items), capacity)
	}
	for i, it := range snap.Items {
		if it.Seq != tail[i].seq || it.Value != tail[i].val {
			t.Fatalf("snapshot[%d]=%+v, want %+v", i, it, tail[i])
		}
	}
	if snap.NextSeq != total+1 {
		t.Fatalf("NextSeq=%d, want %d", snap.NextSeq, total+1)
	}
}
