# slidingmedian

并发安全的固定容量滑动窗口「下中位数」库。纯 Go 标准库实现，无第三方依赖。

## 功能

- 窗口保存 `int64` 值，容量在创建时指定（`capacity > 0`），最多保留最近 `capacity` 个元素。
- 每次成功的 `Add` 分配从 1 开始严格递增、永不重复的 `uint64` Seq；容量满时按 Seq 淘汰最旧元素。
- `Median` 返回下中位数：奇数个元素取中间值，偶数个取靠左的中间值（如 `[1,2,3,4]` 返回 `2`）；空窗口返回 `ErrEmpty`。
- 重复值按 Seq 精确区分，淘汰某个旧元素不会误删值相同的其他元素。
- `Close` 后拒绝新的 `Add`（返回 `ErrClosed`），但 `Median`/`Snapshot`/`Len`/`Capacity` 仍可查询；`Close` 可重复调用。
- 所有方法可安全并发调用。

## 设计

中位数增量维护，不在查询时整体排序：

- **双堆**：`lo` 为大顶堆（值较小的一半），`hi` 为小顶堆（值较大的一半）。保持不变式 `max(lo) <= min(hi)` 且 `len(lo) == len(hi)` 或 `len(lo) == len(hi)+1`（按有效元素计数），因此下中位数即 `lo` 堆顶，`Median` 为 O(1)。
- **滚动窗口**：窗口内 Seq 总是连续区间，最旧元素 Seq 可直接算出，O(1) 定位淘汰目标。
- **延迟删除**：淘汰时只按 Seq 标记 `pending` 并扣减对应堆的有效计数，堆顶清理（`cleanTop`）在后续操作中顺带完成；`Add` 后最多做一次堆间平衡。
- **压缩重建**：当堆中待删除元素超过有效元素时重建两个堆，内存占用始终与容量成正比。
- **并发**：单个 `sync.Mutex` 保护全部内部状态，无后台 goroutine。

## API

```go
w, err := slidingmedian.New(capacity) // capacity <= 0 返回 ErrInvalidCapacity
seq, err := w.Add(value)             // 成功返回 Seq；Close 后返回 ErrClosed
med, err := w.Median()               // 空窗口返回 ErrEmpty
snap := w.Snapshot()                 // Items 按 Seq 升序的独立副本 + Median/NextSeq/Closed
n := w.Len()                         // 当前元素个数
c := w.Capacity()                    // 容量
w.Close()                            // 幂等；之后只读
```

`Snapshot` 返回的是独立副本，修改它不影响窗口内部状态；窗口为空时 `snap.HasMedian` 为 `false`。

## 运行演示

```sh
go run ./cmd/slidingmedian-demo
```

演示逐步 Add、奇偶数量中位数、容量滚动淘汰、重复值精确淘汰以及 Close 后继续读取。

## 运行测试

```sh
go test -race ./...
```
