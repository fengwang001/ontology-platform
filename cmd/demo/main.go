package main

import "fmt"

// 判定骨架：每一项对应交付清单第十节的一条；包实现完成后逐项替换占位实现。
var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
		fails++
	}
}

func basicAllocFree() (string, bool)        { return "基本分配与释放", true }
func mergeLeft() (string, bool)             { return "左邻合并", true }
func mergeRight() (string, bool)            { return "右邻合并", true }
func mergeBoth() (string, bool)             { return "两侧同时合并", true }
func mergedReusable() (string, bool)        { return "合并后可分出更大区间", true }
func reproducible() (string, bool)          { return "同序列重复执行下标相同", true }
func headRemainder() (string, bool)         { return "切分剩余留在尾部(取头部)", true }
func distinctSpaceErrors() (string, bool)   { return "无连续空间/总空闲不够 可区分", true }
func fourErrors() (string, bool)            { return "四类可判定错误", true }
func selfCheckAfterReject() (string, bool)  { return "被拒前后自检通过", true }
func examinedCounts() (string, bool)        { return "碎片100/10000考察数对照", true }
func concurrentSelfCheck() (string, bool)   { return "并发后SelfCheck通过", true }

func main() {
	for _, fn := range []func() (string, bool){
		basicAllocFree, mergeLeft, mergeRight, mergeBoth, mergedReusable,
		reproducible, headRemainder, distinctSpaceErrors, fourErrors,
		selfCheckAfterReject, examinedCounts, concurrentSelfCheck,
	} {
		name, ok := fn()
		check(name, ok)
	}
	if fails != 0 {
		panic("demo checks failed")
	}
}
