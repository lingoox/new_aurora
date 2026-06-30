package middlewares

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// setupTestEnv saves the current Authorization env var and returns a restore func.
func setupTestEnv(val string) func() {
	orig := os.Getenv("Authorization")
	os.Setenv("Authorization", val)
	return func() { os.Setenv("Authorization", orig) }
}

// testHandler is a simple next handler that records it was called.
func testHandler(c *gin.Context) {
	c.Status(http.StatusOK)
}

// TestAuthorizationNoEnvVar verifies that when Authorization env var is not set,
// all requests pass through regardless of header presence.
func TestAuthorizationNoEnvVar(t *testing.T) {
	restore := setupTestEnv("")
	defer restore()

	tests := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"with bearer", "Bearer some-token"},
		{"with random", "xyz123"},
		{"with team suffix", "Bearer token,team456"},
		{"with access token", "Bearer eyJabc.def"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/", nil)
			if tt.header != "" {
				c.Request.Header.Set("Authorization", tt.header)
			}

			Authorization(c)

			if w.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", w.Code)
			}
		})
	}
}

// TestAuthorizationMatchingEnvVar verifies that when Authorization env var is set,
// requests with matching token pass through, and requests without fail.
func TestAuthorizationMatchingEnvVar(t *testing.T) {
	restore := setupTestEnv("my-secret-key")
	defer restore()

	t.Run("no header returns 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)

		Authorization(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("exact match passes", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "my-secret-key")

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("Bearer prefix match passes", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer my-secret-key")

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("case-insensitive Bearer", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "BEARER my-secret-key")

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("wrong token returns 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer wrong-token")

		Authorization(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

// TestAuthorizationTeamSuffix verifies that the optional ,team_account_id suffix
// is parsed and stored in the gin context.
func TestAuthorizationTeamSuffix(t *testing.T) {
	restore := setupTestEnv("my-secret-key")
	defer restore()

	t.Run("team suffix with matching key", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer my-secret-key,team123")

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
		if tid, ok := c.Get("team_account_id"); !ok {
			t.Error("expected team_account_id in context")
		} else if tid != "team123" {
			t.Errorf("expected team_account_id=team123, got %v", tid)
		}
	})

	t.Run("team suffix without matching key returns 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer wrong-key,team456")

		Authorization(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

// TestAuthorizationAccessToken verifies that tokens starting with "eyJ" (access tokens)
// pass through to the handler.
func TestAuthorizationAccessToken(t *testing.T) {
	restore := setupTestEnv("my-secret-key")
	defer restore()

	t.Run("access token passes through", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0")

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
		if tok, ok := c.Get("auth_token"); !ok {
			t.Error("expected auth_token in context")
		} else if tok != "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0" {
			t.Errorf("unexpected auth_token value: %v", tok)
		}
	})

	t.Run("access token with team suffix", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer eyJ0eXAiOiJKV1QifQ,team789")

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
		if tok, ok := c.Get("auth_token"); !ok || tok != "eyJ0eXAiOiJKV1QifQ" {
			t.Errorf("expected auth_token=eyJ0eXAiOiJKV1QifQ, got %v", tok)
		}
		if tid, ok := c.Get("team_account_id"); !ok || tid != "team789" {
			t.Errorf("expected team_account_id=team789, got %v", tid)
		}
	})
}

// TestAuthorizationRefreshToken verifies that long tokens (>64 chars) pass through
// as refresh tokens.
func TestAuthorizationRefreshToken(t *testing.T) {
	restore := setupTestEnv("my-secret-key")
	defer restore()

	// Build a token longer than 64 characters
	longToken := ""
	for i := 0; i < 65; i++ {
		longToken += "a"
	}

	t.Run("refresh token passes through", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer "+longToken)

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
		if tok, ok := c.Get("auth_token"); !ok {
			t.Error("expected auth_token in context")
		} else if tok != longToken {
			t.Errorf("unexpected auth_token value: got %v", tok)
		}
	})

	t.Run("short non-matching token returns 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer short-token")

		Authorization(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

// TestAuthorizationNoBearer verifies handling when no "Bearer " prefix is used.
func TestAuthorizationNoBearer(t *testing.T) {
	restore := setupTestEnv("raw-token-value")
	defer restore()

	t.Run("raw token match without Bearer", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "raw-token-value")

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("raw access token passes through", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "eyJraw-access-token")

		Authorization(c)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}
