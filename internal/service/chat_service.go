package service

import (
	"aurora/internal/accounts"
	"aurora/internal/chatgpt"
	"aurora/internal/config"
	officialtypes "aurora/internal/types/official"
)

// ChatResult 聊天完成的结果
type ChatResult struct {
	Text           string
	ThinkingText   string
	ConversationID string
	Continue       *chatgpt.ContinueInfo
	StopSent       bool
	Sentinel       []map[string]interface{}
}

// maxContinueCount 返回 max_tokens 触发时自动 continue 的最大轮数。
func maxContinueCount(cfg *config.Config) int {
	if cfg != nil {
		return cfg.MaxContinueCount
	}
	return 3
}

// toolCallingEnabled 判断工具调用是否启用
// nil tools 表示"未指定"，视为工具调用可用；空 slice 表示"明确无工具"。
func toolCallingEnabled(tools []officialtypes.Tool, cfg *config.Config) bool {
	// 先检查配置
	if cfg != nil && !cfg.ToolCallingEnabled {
		return false
	}
	// nil 表示请求未指定 tools，工具调用能力仍视为可用
	if tools == nil {
		return true
	}
	return len(tools) > 0
}

// ChatCompletion 执行完整的聊天完成流程
// TODO: 实现完整的编排（sentinel → init → ws → prepare → conversation → continue）
func ChatCompletion(req officialtypes.APIRequest, account *accounts.Account, cfg *config.Config) (*ChatResult, error) {
	return &ChatResult{}, nil
}
