package service

import (
	"aurora/internal/accounts"
	"aurora/internal/config"
	officialtypes "aurora/internal/types/official"
)

// ToolCallResult 工具调用结果
type ToolCallResult struct {
	Text     string
	ToolCalls []officialtypes.ToolCall
}

// ExecuteToolCalling 执行工具调用模拟
// TODO: 注入 tool_call 协议 → conversation → 解析 <tool_call> → 重试
func ExecuteToolCalling(req officialtypes.APIRequest, account *accounts.Account, cfg *config.Config) (*ToolCallResult, error) {
	return &ToolCallResult{}, nil
}
