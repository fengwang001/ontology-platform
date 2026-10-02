# 增量单源最短路服务

Go 包 `ontology` 提供有向正权图上的动态单源最短路维护，支持边新增、改权、删除、当前距离/父边/路径查询以及有限版本历史查询。

## API

```go
svc, err := ontology.New(n, source, emax, keep)
result, err := svc.AddEdge(u, v, w)
result, err := svc.SetWeight(id, w)
result, err := svc.RemoveEdge(id)

distance, reachable, err := svc.Dist(v)
parent, reachable, err := svc.Parent(v)
edgeIDs, reachable, err := svc.Path(v)
distance, reachable, err := svc.DistAt(v, version)
```

`UpdateResult` 包含：

- `Version`：接受更新后的新版本号。
- `EdgeID`：`AddEdge` 新分配的边编号。
- `DChanged`：更新前后距离发生变化（含可达性变化）的升序节点列表。
- `PChanged`：距离不变且可达、确定父边编号变化的升序节点列表。

## 紧边与父边

对存活边 `(u,v,w)`，当且仅当：

```text
d(u) 与 d(v) 都可达，且 d(u) + w = d(v)
```

该边为紧边。每个可达且非源点节点的父边是指向它的所有紧边中编号最小者；源点没有父边。权重严格为正，指向源点的边不可能是紧边。不可达节点没有距离和父边。

## 增量传播规则

- **新增边或降权**：从可能变短的边终点运行局部 Dijkstra，仅松弛会产生更短距离的节点；距离确定后按新距离选择最小编号紧边。距离不变但出现新的等距紧边时，只重算该终点父边。
- **增权或删除**：只处理更新前为当前父边的紧边。先从被影响终点开始，沿更新前的旧紧边关系求闭包：仍有其他未受影响旧紧入边的节点距离不变，只可能切换父边；失去所有旧紧入边的节点进入重算集合。随后以未受影响节点为边界种子，对重算集合运行局部 Dijkstra。
- **非紧边更新**：非紧边增权、非父边紧边删除、原值改权等不会改变距离或父边，只提升版本号。

距离变化的节点只列入 `DChanged`；距离不变但父边变化才列入 `PChanged`。两类互不重叠。

## 编号、容量和错误

- 节点范围：`0..N-1`，`1 <= N <= 2000`。
- 存活边上限：`1 <= Emax <= 100000`。
- 历史保留数：`1 <= K <= 64`。
- 权重范围：`1..1_000_000`。
- 边 ID 从 1 开始严格递增；删除后 ID 不复用，被拒绝的 AddEdge 不消耗 ID。
- 每次被接受的 AddEdge、SetWeight、RemoveEdge 都使版本加 1。

错误使用 `ErrorCode` 区分：

- `InvalidArgument`
- `EdgeNotFound`
- `CapacityFull`
- `HistoryExpired`
- `VersionFuture`

更新操作的校验顺序为参数非法、边不存在、容量满；历史查询按节点非法、版本尚未产生、历史已过期返回。被拒绝操作不修改图、版本、边编号或历史。

## 并发与历史

所有公开方法都在服务内部使用 `sync.RWMWMutex` 串行化；更新持写锁，查询持读锁。查询看到的始终是某个完整版本，不会观察到更新中间态。

版本 0 是只有 N 个孤立节点的初始图。服务保存最近 K 个版本的距离和父边快照；`DistAt` 从不可变快照读取距离。

## 局部性计数

`Examined()` 暴露最近一次接受更新的非导出扫描计数。算法只扫描：

- 更新边及其关联节点；
- 距离/父边可能变化的局部闭包；
- 这些节点的入边、出边以及边界另一端节点的入边。

测试中按题目定义从每次返回的 `DChanged ∪ PChanged` 重建 `F` 与 `H`，校验：

```text
examined <= 4 * (|F| + |H|) + 8
```

## 验证

```bash
GOCACHE=/tmp/go-cache-ontology go test ./...
GOCACHE=/tmp/go-cache-ontology go test -race -v ./...
```

测试包含：

- 题述完整 8 版本示例和历史边界；
- 紧边增权后父边切换、唯一紧边失效级联、替代紧边阻断传播；
- 降权/新增导致可达、距离缩短和后继传播；
- 平行边、SetWeight 原值仍升版本、拒绝不耗编号；
- 1000 节点长链局部性；
- 固定种子 2000 步随机更新，与朴素整图重算逐节点比对距离、父边、路径、变更报告和 examined 上界。

随机测试使用 `go test -v` 时打印每步输入、输出、是否接受以及“reference recomputation”判定依据。
