package service

import (
	"aurora/internal/accounts"
	"aurora/internal/config"
)

// FileResult 上传结果
type FileResult struct {
	FileID   string
	Filename string
	Bytes    int64
	MimeType string
}

// UploadFile 文件上传
// TODO: 调用 chatgpt.UploadFile + RegisterUploadedFile
func UploadFile(account *accounts.Account, cfg *config.Config, data []byte, filename, contentType string) (*FileResult, error) {
	return &FileResult{}, nil
}
