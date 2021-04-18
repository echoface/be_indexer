# Boolean Expression Index

算法描述来源于论文：[Boolean expression index](https://theory.stanford.edu/~sergei/papers/vldb09-indexing.pdf)
为什么写它:
- 大佬写的(优秀的代码)并没有开源
- 网络上能看到的描述和实现模糊不清，完全不能够工程化
- 在线广告、某些功能模块借助其可以实现非常优雅的代码实现
- 论文没有提及的多值查询的问题没有直接给出实现提示，但是实际应用中都特别需要支持

这个Golang版本基本上是之前实现的C++版本的clone,[C++实现移步](https://github.com/HuanGong/ltio/blob/master/components/boolean_indexer)

声明:
   如果被用到或者被应用，请留下star并标明作者出处.

使用限制：
- 每一个document最多拥有255个conjunction
- document id最大限制值为:`2^31`,超过这个数量考虑partition
- 支持值类型非浮点数值、string类型的值(因为背后还是基于值的倒排表)