# provenance — 跨链接双时态溯源查询

在本体平台上查询「某对象在给定有效时间点、经由链接到达的关联对象集合」。
源对象、链接、目标对象三方各自拥有独立的有效时间区间（左闭右开）与写入时间，
路径只有在三方共同约束下可见时才返回；结果稳定、可重复、可追溯。

详细设计、取舍与被放弃方案见 [DESIGN.md](./DESIGN.md)。

## 安装与依赖

纯标准库实现，Go 1.26+，无第三方依赖。

## 快速上手

```go
package main

import (
	"fmt"
	"ontology/provenance"
)

func main() {
	s := provenance.NewStore()

	// 对象 A、B 在写入时间 10 落定，有效区间 [100,200)
	_ = s.WriteObject("A", 10, provenance.Interval{Start: 100, End: 200}, true)
	_ = s.WriteObject("B", 10, provenance.Interval{Start: 100, End: 200}, true)
	// 链接 A->B 写入时间 5
	_ = s.WriteLink("L", 5, provenance.Interval{Start: 100, End: 200}, "A", "B", true)

	// asOf=10 时三方都已落定，validAt=150 被三方区间覆盖
	res, err := s.Traverse(provenance.Query{
		Source: "A", ValidAt: 150, AsOf: 10, MaxDepth: 1,
	})
	if err != nil {
		panic(err)
	}
	for _, p := range res.Paths {
		fmt.Println(p.Nodes) // [A B]
	}
}
```

## 核心 API

| API | 说明 |
| --- | --- |
| --- | --- |
| `NewStore()` | 创建只追加的双时态存储 |
| `(*Store).WriteObject(id, writeAt, interval, exists)` | 新建/修正对象；写入时间必须严格晚于上一条 |
| `(*Store).WriteLink(id, writeAt, interval, src, tgt, exists)` | 新建/修正链接；端点不可变 |
| `(*Store).Traverse(Query)` | 多跳 AsOf 溯源，返回路径、拒绝项与候选计数 |
| `NewQueryLogger(w)` / `.Log` / `.LogError` | JSON Lines 审计日志 |
| `NewNaiveStore()` | 独立全扫描参照实现，仅用于差分测试 |

### 不可见原因

- `ReasonNotEstablished`：给定有效时间点没有任何覆盖记录（此刻未建立）。
- `ReasonNotYetVisible`：存在覆盖该有效时间点的记录，但其写入时间晚于
  `AsOf`（已建立但对本次查询尚不可见）。

### 错误（固定、互斥的判定次序）

1. `ErrSourceNotFound` 2. `ErrInvalidTime` 3. `ErrInvalidDepth`
4. `ErrAsOfBeforeSource`。

## 测试

```bash
export GOCACHE=/tmp/gocache   # 仅当默认 go 构建缓存目录只读时需要
go test -race -v ./provenance
go test -coverprofile=cov.out ./provenance && go tool cover -func=cov.out
```
