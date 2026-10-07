# slidingmedian

并发安全的定容滑动窗口**下中位数**库（纯 Go 标准库，无第三方依赖、无网络、无后台 goroutine）。

## 特性

- 固定容量滑动窗口，保存最近 `capacity` 个 `int64`；满后按 Seq 淘汰最旧元素
- 每次成功的 `Add` 分配从 1 开始严格递增、永不重复的 `uint64` Seq
- 双堆（最大堆 + 最小堆）+ 按 Seq 延迟删除，增量维护中位数：
  `Add` / 淘汰 / 平衡为 `O(log n)`，`Median` 摊还 `O(1)`，从不整体重排
- 重复值按 `(value, seq)` 区分，淘汰只移除目标 Seq，不误删同值元素
- 所有方法可并发调用（互斥锁保护），`go test -race` 通过
- `Close` 幂等；关闭后拒绝 `Add`（返回 `ErrClosed`），已有数据仍可读

## API

```go
w, err := slidingmedian.NewWindow(3)   // capacity <= 0 返回 ErrInvalidCapacity
seq, err := w.Add(42)                  // 返回分配的 Seq；关闭后返回 ErrClosed
med, err := w.Median()                 // 下中位数；空窗口返回 ErrEmpty
snap := w.Snapshot()                   // 按 Seq 升序的独立副本 + Median/NextSeq/Closed
n := w.Len()                           // 当前元素个数
c := w.Capacity()                      // 容量
w.Close()                              // 幂等；关闭后查询仍可用
```

下中位数定义：元素个数为奇数时取中间元素，为偶数时取靠左的中间元素
（`[1,2,3,4] -> 2`）。

## CLI 演示

```sh
go run ./cmd/slidingmedian
```

逐步演示：奇偶数量中位数、容量滚动淘汰、重复值精确淘汰、`Close` 后继续读取。

## 测试

```sh
go test -race ./...
```

覆盖：奇偶中位数、容量滚动淘汰、重复值精确淘汰、多轮堆平衡（对照暴力排序）、
`int64` 边界值、Snapshot 独立性、`Close` 语义与基础并发。
