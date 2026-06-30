package main

import (
	"os"
	"strings"

	"aurora/internal/accounts"
	"aurora/internal/browserfp"
	"aurora/internal/config"
	"aurora/internal/handler"
	"aurora/internal/proxy"

	"github.com/gin-gonic/gin"

	"github.com/g-utils/endless"
	"github.com/joho/godotenv"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	_ = godotenv.Load(".env")
	browserfp.Init()

	cfg := config.Load()

	// 初始化代理池和账号池
	proxies := loadProxyList()
	proxyPool := proxy.NewPool(proxies, "")
	_ = proxyPool // 账号创建时按需分配

	accs := accounts.InitAccountsFromConfig(
		"access_tokens.txt",
		"free_tokens.txt",
		cfg.FreeAccounts,
		cfg.FreeAccountsNum,
		accounts.DefaultProfiles,
	)
	// 为每个 account 初始化 TLS Client
	for _, acct := range accs {
		_ = acct.InitClient()
	}
	accountPool := accounts.NewPool(accs)

	router := handler.RegisterRouter(accountPool)

	host := cfg.ServerHost
	port := cfg.ServerPort
	tlsCert := cfg.TLSCert
	tlsKey := cfg.TLSKey

	if host == "" {
		host = "0.0.0.0"
	}
	if port == "" {
		port = "8080"
	}

	if tlsCert != "" && tlsKey != "" {
		_ = endless.ListenAndServeTLS(host+":"+port, tlsCert, tlsKey, router)
	} else {
		_ = endless.ListenAndServe(host+":"+port, router)
	}
}

// loadProxyList 从 proxies.txt / PROXY_URL / http_proxy 加载代理列表
// 替代旧的 initialize/proxy.go:checkProxy
func loadProxyList() []string {
	var proxies []string
	proxyUrl := os.Getenv("PROXY_URL")
	if proxyUrl != "" {
		proxies = append(proxies, proxyUrl)
	}

	if _, err := os.Stat("proxies.txt"); err == nil {
		data, err := os.ReadFile("proxies.txt")
		if err == nil {
			lines := string(data)
			for _, line := range strings.Split(lines, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					proxies = append(proxies, line)
				}
			}
		}
	}

	if len(proxies) == 0 {
		proxy := os.Getenv("http_proxy")
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}

	return proxies
}
