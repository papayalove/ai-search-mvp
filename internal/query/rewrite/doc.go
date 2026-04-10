// Package rewrite 对应「Query Rewrite」检索侧：策略（提示词、解析）、Rewriter 接口、先改写再召回。
//
// 实现位置：
//   - 底层 Chat HTTP：internal/model/rewrite.ChatClient；流式按「行」推子查询（SSE rewrite_query）
//   - 本包：Rewriter、LLMRewriter、RunTextRetrievalWithOptionalRewrite（日志前缀 search: request_id=…）
//   - 纯并行合并：internal/query/recall
//   - 环境装配：internal/config.LoadRewriterFromEnv → NewLLMRewriter(ChatClient)
//   - POST /v1/search：internal/query/pipeline.MilvusSearcher.Rewriter
//
// PathModelRewrite 为底层 Chat 客户端路径锚点。
package rewrite

// PathModelRewrite 大模型 Chat 客户端代码路径（相对模块根）。
const PathModelRewrite = "internal/model/rewrite"
