package service

import (
	"os"
	"testing"

	officialtypes "aurora/internal/types/official"
)

func TestContinueCountDefault(t *testing.T) {
	// Ensure env var is unset
	os.Unsetenv("MAX_CONTINUE_COUNT")
	if maxContinueCount() != 3 {
		t.Errorf("default continue count should be 3")
	}
}

func TestToolCallingEnabled(t *testing.T) {
	os.Unsetenv("TOOL_CALLING_ENABLED")
	if !toolCallingEnabled(nil) {
		t.Error("toolCallingEnabled(nil) should be true when env not set")
	}
}

func TestContinueCountCustom(t *testing.T) {
	os.Setenv("MAX_CONTINUE_COUNT", "5")
	defer os.Unsetenv("MAX_CONTINUE_COUNT")
	if maxContinueCount() != 5 {
		t.Errorf("expected 5, got %d", maxContinueCount())
	}
}

func TestContinueCountZero(t *testing.T) {
	os.Setenv("MAX_CONTINUE_COUNT", "0")
	defer os.Unsetenv("MAX_CONTINUE_COUNT")
	if maxContinueCount() != 0 {
		t.Errorf("expected 0, got %d", maxContinueCount())
	}
}

func TestContinueCountInvalid(t *testing.T) {
	os.Setenv("MAX_CONTINUE_COUNT", "invalid")
	defer os.Unsetenv("MAX_CONTINUE_COUNT")
	if maxContinueCount() != 3 {
		t.Errorf("expected 3 (default) for invalid value, got %d", maxContinueCount())
	}
}

func TestToolCallingEnabledFalse(t *testing.T) {
	os.Setenv("TOOL_CALLING_ENABLED", "false")
	defer os.Unsetenv("TOOL_CALLING_ENABLED")
	if toolCallingEnabled([]officialtypes.Tool{{Type: "function"}}) {
		t.Error("toolCallingEnabled should be false when env is false")
	}
}

func TestToolCallingEnabledNoTools(t *testing.T) {
	os.Unsetenv("TOOL_CALLING_ENABLED")
	if toolCallingEnabled([]officialtypes.Tool{}) {
		t.Error("toolCallingEnabled should be false with empty tools slice")
	}
}
