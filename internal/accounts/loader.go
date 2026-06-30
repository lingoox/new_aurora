package accounts

import (
	"bufio"
	"math/rand"
	"os"
	"strings"

	"github.com/google/uuid"
)

// LoadedSecret 从文件加载的 token 信息
type LoadedSecret struct {
	Token  string
	TeamID string
	IsFree bool
}

// LoadTokensFromFile 从文件读取 token，兼容原格式
// 空行和 # 开头的行被忽略
func LoadTokensFromFile(path string) []LoadedSecret {
	f, err := os.Open(path)
	if err != nil {
		return []LoadedSecret{}
	}
	defer f.Close()

	var secrets []LoadedSecret
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		token := strings.TrimSpace(parts[0])
		if token == "" {
			continue
		}
		secret := LoadedSecret{Token: token}
		if len(parts) > 1 {
			secret.TeamID = strings.TrimSpace(parts[1])
		}
		secrets = append(secrets, secret)
	}
	return secrets
}

// InitAccountsFromConfig 根据配置初始化账号池
// 从 access_tokens.txt / free_tokens.txt 加载，必要时生成 free UUID
func InitAccountsFromConfig(
	accessTokenPath string,
	freeTokenPath string,
	freeAccounts bool,
	freeAccountsNum int,
	profilePool []FingerprintProfile,
) []*Account {
	var accounts []*Account

	// 加载 paid token
	for _, s := range LoadTokensFromFile(accessTokenPath) {
		acct := NewAccount(uuid.NewString(), TypePUID, s.Token)
		if s.TeamID != "" {
			acct.TeamUserID = s.TeamID
		}
		acct.Fingerprint = randomProfile(profilePool)
		acct.Status = StatusActive
		accounts = append(accounts, acct)
	}

	// 加载 free token
	for _, s := range LoadTokensFromFile(freeTokenPath) {
		acct := NewAccount(uuid.NewString(), TypeFree, s.Token)
		acct.Fingerprint = randomProfile(profilePool)
		acct.Status = StatusActive
		accounts = append(accounts, acct)
	}

	// 生成 free UUID 账号
	if freeAccounts {
		for i := 0; i < freeAccountsNum; i++ {
			uid := uuid.NewString()
			acct := NewAccount(uid, TypeNoAuth, uid)
			acct.Fingerprint = randomProfile(profilePool)
			acct.Status = StatusActive
			accounts = append(accounts, acct)
		}
	}

	return accounts
}

func randomProfile(profiles []FingerprintProfile) BrowserFingerprint {
	if len(profiles) == 0 {
		return BrowserFingerprint{
			OaiDeviceID:  uuid.NewString(),
			OaiSessionID: uuid.NewString(),
		}
	}
	p := profiles[rand.Intn(len(profiles))]
	return BrowserFingerprint{
		OaiDeviceID:         uuid.NewString(),
		OaiSessionID:        uuid.NewString(),
		UserAgent:           p.UserAgent,
		ScreenWidth:         p.ScreenWidth,
		ScreenHeight:        p.ScreenHeight,
		HardwareConcurrency: p.HardwareConcurrency,
		Platform:            p.Platform,
		TLSProfileName:      p.TLSProfileName,
	}
}
