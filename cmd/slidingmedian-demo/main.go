// Command slidingmedian-demo demonstrates the slidingmedian library:
// step-by-step adds, odd/even medians, rolling eviction, duplicate values
// and reads after Close.
package main

import (
	"fmt"
	"strings"

	"slidingmedian"
)

func main() {
	fmt.Println("== 1. 逐步 Add，观察奇/偶数量中位数 (capacity=4) ==")
	w := must(slidingmedian.New(4))
	for _, v := range []int64{10, 20, 30, 40} {
		add(w, v)
	}

	fmt.Println("\n== 2. 容量已满，按 Seq 淘汰最旧元素 ==")
	for _, v := range []int64{50, 60} {
		add(w, v)
	}

	fmt.Println("\n== 3. 重复值精确淘汰 (capacity=3) ==")
	d := must(slidingmedian.New(3))
	for _, v := range []int64{8, 8, 8, 9, 9} {
		add(d, v)
	}

	fmt.Println("\n== 4. Close 后拒绝写入，读取仍然可用 ==")
	d.Close()
	if _, err := d.Add(100); err != nil {
		fmt.Printf("Add(100) 被拒绝: %v\n", err)
	}
	d.Close() // 重复 Close 安全
	state("Close 后再查询", d)
}

func must(w *slidingmedian.Window, err error) *slidingmedian.Window {
	if err != nil {
		panic(err)
	}
	return w
}

func add(w *slidingmedian.Window, v int64) {
	seq, err := w.Add(v)
	if err != nil {
		panic(err)
	}
	state(fmt.Sprintf("Add(%d) -> seq=%d", v, seq), w)
}

func state(label string, w *slidingmedian.Window) {
	snap := w.Snapshot()
	parts := make([]string, 0, len(snap.Items))
	for _, it := range snap.Items {
		parts = append(parts, fmt.Sprintf("%d(seq=%d)", it.Value, it.Seq))
	}
	median := "空"
	if snap.HasMedian {
		median = fmt.Sprintf("%d", snap.Median)
	}
	fmt.Printf("%-16s 窗口=[%s] Len=%d/%d Median=%s NextSeq=%d Closed=%v\n",
		label, strings.Join(parts, " "), w.Len(), w.Capacity(), median, snap.NextSeq, snap.Closed)
}
