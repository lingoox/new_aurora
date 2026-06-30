package service

import (
	"aurora/internal/accounts"
	"aurora/internal/config"
	officialtypes "aurora/internal/types/official"
)

// ImageResult 图片生成结果
type ImageResult struct {
	Data         []officialtypes.ImageGenerationData
	UpstreamText string
}

// GenerateImages 图片生成
// TODO: 完整编排 sentinel → generate → download
func GenerateImages(req officialtypes.ImageGenerationRequest, account *accounts.Account, cfg *config.Config) (*ImageResult, error) {
	return &ImageResult{}, nil
}

// GenerateImageEdit 图片编辑/变体
// TODO: 完整编排 sentinel → upload references → generate → download
func GenerateImageEdit(prompt string, account *accounts.Account, cfg *config.Config, references interface{}) (*ImageResult, error) {
	return &ImageResult{}, nil
}
