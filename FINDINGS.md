# FINDINGS：ttlcache 实现行为与文档/直觉承诺的偏差

下列每条均为当前实现的**真实行为**（已由 `internal/ttlcache/characterization_test.go`
钉住，全部通过），并非建议修复方案。时间单位均为注入时钟的毫秒刻度。

## 1. 驱逐过期项按 `createdAt` 排序，而非「过期最早」

- 位置：`internal/ttlcache/cache.go` `evict`
  （谓词 `e.createdAt < oldestExpired.createdAt`）。
- 复现输入：容量 2；`Put("a", ttl=100)` 于 t=0，`Put("b", ttl=10)` 于 t=5；
  时钟走到 t=105（a 在 t=100 过期、b 在 t=15 过期），再 `Put("c")` 触发驱逐。
- 实际输出：驱逐 `a`（写入最早，createdAt=0）；`b` 虽在 t=15 就已过期（比 a
  早 85 个刻度），仍留在缓存中。
- 应当输出（按 LRU+TTL 直觉 / 「过期最早」口径）：驱逐 expireAt 最小的 `b`，
  保留过期更晚的 `a`。
- 根因：`evict` 在过期候选中比较的是 `createdAt`，而每项 TTL 不同，
  `createdAt` 早 ≠ `expireAt`（`createdAt+ttl`）早；长短 TTL 交错时两种口径
  顺序相反。注释只说「写入时刻最早」，未说明这与「过期最早」的差异。

## 2. 重复 `Put` 不延寿；对已过期键重写等于写入一个立刻过期的值

- 位置：`internal/ttlcache/operations.go` `Put`
  （命中已存在键时仅 `e.val = val` + `moveToFront(e)`）。
- 复现输入：`Put("a", ttl=10)` 于 t=0；t=10（已到过期边界）后
  `Put("a", "new", ttl=1000)`，随后立即 `Get("a")`。
- 实际输出：`Get("a")` 返回 miss；`Len()` 在该 Get 之前仍为 1。即新 TTL
  1000 完全无效，条目仍按 t=10 过期。到期前（t=5）重 Put 大 TTL 同理，t=10 必过期。
- 应当输出（按调用方直觉）：重写以新值、新 TTL 重新计时（createdAt=now，
  expireAt=1010），`Get("a")` 命中 `"new"`。
- 根因：更新分支不写 `createdAt`/`ttl`；且过期项未被惰性清理前仍在 `items`
  中，重写走「更新」而非「新建」分支，连「借重写复活」都做不到。
  `entry.go` 注释虽提到不刷新，但未说明「过期后重写也不复活」这一后果。

## 3. 惰性单条驱逐：多个过期项长期占位，并挤掉有效容量

- 位置：`internal/ttlcache/cache.go` `evict`（每次只 `removeEntry` 一个）；
  `operations.go` `Put`（仅 `len(items) >= capacity` 时调用一次）。
- 复现输入：容量 3，连续 Put `k0/k1/k2`（ttl=10）；时钟走到 t=10 全部过期；
  `Put("fresh", ttl=1000)`。
- 实际输出：`Len()` 仍为 3；3 个旧键中恰有 1 个被驱逐，其余 2 个过期项继续
  占容量，且不被同一次 Put 清理；每次后续 Put/Get/Delete 最多只清理 1 个。
- 应当输出（按「TTL 缓存」直觉）：插入前应清出足够容量（驱逐全部已过期项或
  至少驱逐到能容纳新项），过期项不应长期占用容量、把有效项的位置吃掉。
- 根因：没有后台/批量过期清理；`evict` 设计为「每次恰好删一个」，`Len` 又把
  过期项计入，导致过期垃圾与有效项争抢固定容量。文档只说 `Len` 含过期项，
  未承诺批量清理策略。

## 4. 同 `createdAt` 的过期项：头侧（MRU）反而先被驱逐

- 位置：`internal/ttlcache/cache.go` `evict`
  （从头向尾扫描 + 严格小于 `<`）。
- 复现输入：时钟保持 t=0，容量 2，依次 `Put("a")`、`Put("b")`（两者
  createdAt 均为 0、ttl=10；b 在头、a 在尾）；时钟走到 t=10 后 `Put("c")`。
  变体：到期前 `Get("a")` 把头换成 a，被驱逐者随之变成 a。
- 实际输出：驱逐头侧（最近使用）的 `b`，尾侧（最久未使用）的 `a` 保留。
- 应当输出（按 LRU 直觉）：写入时刻相同时保留 MRU、驱逐 LRU（尾侧 `a`）。
- 根因：扫描方向 head→tail 使头侧候选先入 `oldestExpired`；比较用严格小于，
  后续相等 createdAt 的项无法替换候选，于是「先遇到的头侧项」被锁定驱逐，
  与回退分支「无过期项时驱逐 tail（LRU）」的口径恰好相反。

## 5. 过期项在 Get / Delete / Len 三入口口径不一致

- 位置：`internal/ttlcache/operations.go`。
- 复现输入：容量 2，`Put("a", ttl=10)` 于 t=0，时钟走到 t=10（恰过期），
  分别只执行一种操作：
  - `Get("a")`
  - `Delete("a")`
  - `Len()`
- 实际输出：
  - `Get`：返回 `("", false)`（按 miss）并**立即删除**，随后 `Len()` 为 0；
  - `Delete`：返回 **true**（过期项仍算「存在」）并删除，随后 `Len()` 为 0；
  - `Len`：返回 **1**，过期项被计数且**不被清理**，之后 `Delete("a")` 仍为 true。
- 应当输出（一致性口径，二选一并写明）：要么所有入口都把过期项视为已不存在
  （Get miss 且删除、Delete 返回 false、Len 不计数），要么统一保留惰性语义但
  在文档中明确三者差异。
- 根因：只有 `Get` 调用 `expiredAt` 判断并 `removeEntry`；`Delete` 与 `Len`
  完全不看时间，直接操作 map。三处行为散落在不同方法中，注释虽各自提及，
  但未指出三者对「同一过期项」给出的存在性结论互相矛盾。
