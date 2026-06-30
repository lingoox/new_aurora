package service

import (
	"os"
	"strconv"
	"strings"

	"aurora/internal/accounts"
	"aurora/internal/chatgpt"
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

// maxContinueCount 从环境变量读取（后续改为从 Config 获取）
func maxContinueCount() int {
	v := os.Getenv("MAX_CONTINUE_COUNT")
	if v == "" {
		return 3
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 3
	}
	return n
}

// toolCallingEnabled 判断工具调用是否启用
// nil tools 表示"未指定"，视为工具调用可用；空 slice 表示"明确无工具"。
func toolCallingEnabled(tools []officialtypes.Tool) bool {
	if env := strings.ToLower(strings.TrimSpace(os.Getenv("TOOL_CALLING_ENABLED"))); env == "false" || env == "0" || env == "no" {
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
func ChatCompletion(req officialtypes.APIRequest, account *accounts.Account) (*ChatResult, error) {
	return &ChatResult{}, nil
}
