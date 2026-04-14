// Package entity 提供面向检索的轻量关键词与 query 信号抽取（RAKE 风格，纯 Go、无外部 NLP 依赖）。
//
// 正文：ExtractKeywordsFromArticle 返回按分数排序的短语（类似 rake_nltk 的 get_ranked_phrases）。
// 检索 query：ExtractFromSearchQuery 返回实体倾向片段、关系提示词与 RAKE 关键词。
package entity
