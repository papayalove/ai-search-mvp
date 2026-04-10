// Package rewrite 对应设计文档中的「Query Rewrite」编排层命名空间。
//
// 实现位置：
//   - 大模型客户端：internal/model/rewrite（阿里云 DashScope OpenAI 兼容 Chat）
//   - 多路并行召回与降级：internal/query/recall.RunTextRetrievalWithOptionalRewrite、RunParallelTextRetrieval
//   - POST /v1/search 接入：internal/query/pipeline.MilvusSearcher.Rewriter
//
// PathModelRewrite 为文档锚点常量。
package rewrite

// PathModelRewrite 大模型 rewrite 客户端代码路径（相对模块根）。
const PathModelRewrite = "internal/model/rewrite"
