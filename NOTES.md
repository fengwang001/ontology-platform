# NOTES

## 八步推导（numFrames=3；c=clean, d=dirty, -空；状态格式 (page,dirty,pin)）

| # | 操作 | 返回帧 | 帧0 | 帧1 | 帧2 | writes |
|---|---|---|---|---|---|---|
| 1 | Pin(1) | 0 | (1,c,1) | - | - | 0 |
| 2 | Pin(2) | 1 | (1,c,1) | (2,c,1) | - | 0 |
| 3 | Pin(3) | 2 | (1,c,1) | (2,c,1) | (3,c,1) | 0 |
| 4 | MarkDirty(0) | - | (1,d,1) | (2,c,1) | (3,c,1) | 0 |
| 5 | Unpin(0) | - | (1,d,0) | (2,c,1) | (3,c,1) | 0 |
| 6 | Pin(4) | 0（驱逐脏 page1，先写回） | (4,c,1) | (2,c,1) | (3,c,1) | 1 |
| 7 | Unpin(1) | - | (4,c,1) | (2,c,0) | (3,c,1) | 1 |
| 8 | Pin(5) | 1（驱逐 clean page2，不写回） | (4,c,1) | (5,c,1) | (3,c,1) | 1 |

- (甲) 帧1 的 page2 仍 pin=1，却被覆盖清除：它的持有者再 Pin(2) 会装入一份全新 clean 副本，旧页（含未写回修改）丢失，同一 pageId 出现视图不一致；正确实现只能驱逐帧0。
- (乙) 正确：先写回脏 page1，writes=1。不写回直接覆盖则 page1 的修改永久丢失，writes=0。
- (丙) 连续两次 Unpin 同一帧 → pin=-1；负 pin 被当成可驱逐，后续 Pin 会误驱逐该帧中仍在使用的页，造成在用页被覆盖。

## 不变量保证位置与钉住测试

1. 唯一性与守恒：pool.go Pin 的 index map（pageId→frame）+ 驱逐时 delete 旧映射；TestUniquenessConservation。
2. 与朴素参照一致：pool.go Pin/Unpin/MarkDirty 的状态迁移；api.go SelfCheck 内置朴素模拟逐拍对比；TestNaiveReference、TestSelfCheck。
3. pin 安全：frame.go Frame.Evictable（pin==0 才可驱逐）与 pool.go 驱逐选帧；TestPinnedNeverEvicted。
4. 失败不留痕：pool.go 全部参数校验（越界、pin==0、无空帧且全 pin）在任何写入之前；TestRejectedOpsLeaveNoTrace、TestErrorsDistinct。
