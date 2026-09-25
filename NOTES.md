# 旋转排序数组二分查找：推导笔记

## 分支逻辑推导（先判哪半边有序）
旋转排序数组（严格递增序列循环右移 k 位）取 mid=(lo+hi)/2 后，[lo,mid] 与
[mid,hi] 至少一半严格有序：旋转点唯一，只落在其中一半，另一半完整保序。
故每轮：1) 用 nums[lo] <= nums[mid] 判哪半有序（成立则左半，否则右半）；
2) 再判 target 是否落在有序半值域内（左半看 nums[lo] <= target < nums[mid]，
右半看 nums[mid] < target <= nums[hi]），落内缩到该半，否则缩到另一半。

## 反例：直接比 nums[mid] 与 target 会漏查
普通二分隐含「全局有序」前提，旋转后失效。nums=[4,5,6,7,0,1,2], target=0：
nums[3]=7>0 去左半，nums[1]=5>0 去 [4]，nums[0]=4>0 区间空，返回 -1；
但 0 在下标 4（旋转点另一侧），第一步即被跳过。钉住：Search(...,0)==4、
Search(...,3)==-1；TestNaiveBinaryMisses 内联该错误实现并断言其漏查。

## 比较次数口径与上界
只计与 target 的关键比较（三态计 1 次）：每轮比 nums[mid] 一次，符号已知后
判值域只需再补 1 次边界比较，每轮 <= 2 次；轮数 <= floor(log2 n)+1，故
<= 2*log2(n)+4。半边有序性判断是结构比较，不计入。见 TestComparisonBound。

## 语义 → 代码/测试位置
- 命中/未命中：rot/rot.go Search；TestSearchTable、TestAgainstLinear
- 对拍正确性：check/check.go Linear；TestAgainstLinear（10000 组）
- 空数组 -1 不报错：arr/arr.go Search；TestSearchTable、cmd/demo
- 非严格旋转自洽：arr.Validate 哨兵错误；TestArrValidate（errors.Is）
- 边界（单元素/无旋转）：TestSearchTable
- 并发安全：rot 只读输入 + atomic 计数器；TestConcurrent（-race）
