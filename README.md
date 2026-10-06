# ontology-platform

对象保留与回收子系统位于根包（Go 包名为 `ontology`）。

## 模型与操作

- `NewStore(policy)` 校验可达档、不可达档和新鲜宽限，不能使用负期限。
- `AddContent` / `AddCommit` 注册对象；提交父项和内容引用必须已经存在。
- `Set(name, commit, now, operator)` 创建或更新引用，旧值自动来自上一条日志。
- `Delete(name, now, operator)` 追加新值为空的删除记录；引用墓碑会保留。
- `History(name, reverseIndex)` 按 `1=最新` 读取未过期日志。
- `Expire(now)` 只做日志过期；`Collect(now)` 只按当前时刻临时判定过期并回收。
- `ExpireAndCollect(now)` 先过期后回收，结果等同于分开依次调用。

## 保留规则

- 引用当前值永远是存活根。
- 未过期日志的旧值和新值都是存活根；过期日志不再保活。
- 旧值是当前值祖先（含相等）时使用 `ReachableRetention`，否则使用 `UnreachableRetention`。
- 已删除引用的当前值为空，其所有记录都使用不可达档。
- 年龄 `now-at >= retention` 即过期，边界取等。
- 候选对象还需满足 `now-written >= FreshGrace` 才删除，边界取等。
- 回收遍历父提交边和提交到内容边，返回删除的提交数、内容数和字节总量。

## 一致性与性能

所有时刻由调用方提供；小于上一次接受时刻的调用返回 `ErrClockMovedBack`，且不产生副作用。
写操作通过同一把互斥锁串行化，查询使用读锁；相同时间戳的引用更新按成功获得写锁的先后入日志。

提交维度维护保活计数，单个提交是否为根不需要扫描日志。日志调度使用全局到期小顶堆；
引用更新造成归档变化时，通过引用版本号发布新的不可变调度项，旧项惰性失效。

## 文档

- `DESIGN.md`：关键取舍、放弃方案、复杂度与本地验证说明。

## 本地验证

当前环境默认 Go 构建缓存目录只读，可用：

```bash
GOCACHE=/tmp/go-build-cache go test ./...
GOCACHE=/tmp/go-build-cache go test -race ./...
GOCACHE=/tmp/go-build-cache go vet ./...
```

测试包含两档取等、合并祖先、新鲜宽限、保活失效、幂等、删除引用、同名重建、
时钟回退、错误优先、并发串行等价、同刻更新顺序，以及随机操作对照朴素模型。
