package api

import (
	"aurora/internal/accounts"
	"aurora/internal/config"
	"aurora/internal/handler"

	"github.com/gin-gonic/gin"
	"net/http"
)

var router *gin.Engine

func init() {
	cfg := config.Load()
	accs := accounts.InitAccountsFromConfig(
		"access_tokens.txt",
		"free_tokens.txt",
		cfg.FreeAccounts,
		cfg.FreeAccountsNum,
		accounts.DefaultProfiles,
	)
	for _, acct := range accs {
		_ = acct.InitClient()
	}
	accountPool := accounts.NewPool(accs)
	router = handler.RegisterRouter(accountPool, &cfg)
}

func Listen(w http.ResponseWriter, r *http.Request) {
	router.ServeHTTP(w, r)
}
