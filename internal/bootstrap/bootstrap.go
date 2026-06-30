package bootstrap

import (
	"os"
	"strings"

	"aurora/internal/accounts"
	"aurora/internal/browserfp"
	"aurora/internal/config"
	"aurora/internal/handler"
	"aurora/internal/proxy"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// App 封装应用启动所需的所有依赖
type App struct {
	Router      *gin.Engine
	Config      *config.Config
	AccountPool *accounts.Pool
}

// Init 完成所有初始化逻辑，返回 App 实例
func Init() (*App, error) {
	gin.SetMode(gin.ReleaseMode)
	_ = godotenv.Load(".env")
	browserfp.Init()

	cfg := config.Load()

	// 初始化代理池
	proxies := loadProxyList()
	proxyPool := proxy.NewPool(proxies, "")
	_ = proxyPool // 账号创建时按需分配

	// 初始化账号池
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

	// 注册路由
	router := handler.RegisterRouter(accountPool, &cfg)

	return &App{
		Router:      router,
		Config:      &cfg,
		AccountPool: accountPool,
	}, nil
}

// loadProxyList 从 proxies.txt / PROXY_URL / http_proxy 加载代理列表
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
