package service

import (
	"aurora/internal/accounts"
	"aurora/internal/config"
	officialtypes "aurora/internal/types/official"
)

// AudioResult 语音服务结果
type AudioResult struct {
	Data []byte
	Text string
}

// SynthesizeSpeech TTS 合成
// TODO: 完整编排 conversation → get audio
func SynthesizeSpeech(req officialtypes.TTSAPIRequest, account *accounts.Account, cfg *config.Config) (*AudioResult, error) {
	return &AudioResult{}, nil
}

// TranscribeAudio 语音转文字
// TODO: 调用 chatgpt.TranscribeAudio
func TranscribeAudio(account *accounts.Account, cfg *config.Config, audioData []byte, filename, mimeType, language string) (*AudioResult, error) {
	return &AudioResult{}, nil
}
