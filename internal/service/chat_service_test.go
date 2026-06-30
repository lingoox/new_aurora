package service

import (
	"testing"

	"aurora/internal/config"
	officialtypes "aurora/internal/types/official"
)

func TestContinueCountDefault(t *testing.T) {
	cfg := &config.Config{MaxContinueCount: 3, ToolCallingEnabled: true}
	if n := maxContinueCount(cfg); n != 3 {
		t.Errorf("default continue count should be 3, got %d", n)
	}
}

func TestContinueCountNilConfig(t *testing.T) {
	if n := maxContinueCount(nil); n != 3 {
		t.Errorf("nil config should default to 3, got %d", n)
	}
}

func TestToolCallingEnabled(t *testing.T) {
	cfg := &config.Config{MaxContinueCount: 3, ToolCallingEnabled: true}
	if !toolCallingEnabled(nil, cfg) {
		t.Error("toolCallingEnabled(nil) should be true when enabled")
	}
}

func TestContinueCountCustom(t *testing.T) {
	cfg := &config.Config{MaxContinueCount: 5, ToolCallingEnabled: true}
	if n := maxContinueCount(cfg); n != 5 {
		t.Errorf("expected 5, got %d", n)
	}
}

func TestContinueCountZero(t *testing.T) {
	cfg := &config.Config{MaxContinueCount: 0, ToolCallingEnabled: true}
	if n := maxContinueCount(cfg); n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestToolCallingEnabledFalse(t *testing.T) {
	cfg := &config.Config{MaxContinueCount: 3, ToolCallingEnabled: false}
	if toolCallingEnabled([]officialtypes.Tool{{Type: "function"}}, cfg) {
		t.Error("toolCallingEnabled should be false when config disables it")
	}
}

func TestToolCallingEnabledNoTools(t *testing.T) {
	cfg := &config.Config{MaxContinueCount: 3, ToolCallingEnabled: true}
	if toolCallingEnabled([]officialtypes.Tool{}, cfg) {
		t.Error("toolCallingEnabled should be false with empty tools slice")
	}
}

func TestToolCallingEnabledNilTools(t *testing.T) {
	cfg := &config.Config{MaxContinueCount: 3, ToolCallingEnabled: true}
	if !toolCallingEnabled(nil, cfg) {
		t.Error("toolCallingEnabled(nil) should be true when config says enabled")
	}
}
