package chatgpt

import (
	"aurora/httpclient"
	"aurora/internal/browserfp"
	"aurora/internal/prooftoken"
	"aurora/internal/so"
	"aurora/internal/accounts"
	"aurora/util"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

var BaseURL string

func init() {
	_ = godotenv.Load(".env")
	BaseURL = os.Getenv("BASE_URL")
	if BaseURL == "" {
		BaseURL = "https://chatgpt.com/backend-api"
	}
	cores := []int{8, 12, 16, 24}
	screens := []int{3000, 4000, 6000}
	rand.New(rand.NewSource(time.Now().UnixNano()))
	core := cores[rand.Intn(4)]
	rand.New(rand.NewSource(time.Now().UnixNano()))
	screen := screens[rand.Intn(3)]
	cachedHardware = core + screen
}

var (
	API_REVERSE_PROXY   = os.Getenv("API_REVERSE_PROXY")
	FILES_REVERSE_PROXY = os.Getenv("FILES_REVERSE_PROXY")
	// oaiDeviceID / oaiSessionID 进程启动时随机生成。
	// 每次进程启动都重新生成,保持"每次运行都是新设备"的风控画像,
	// 避免多个进程/部署共享同一个指纹导致关联降权。
	// 不落盘:二进制发布到不同机器时指纹天然不同。
	oaiDeviceID        = uuid.NewString()
	oaiSessionID       = uuid.NewString()
	oaiStartTime       = time.Now()
	timeLayout         = "Mon Jan 2 2006 15:04:05"
	BasicCookies       []*http.Cookie
	cachedHardware     = 0
	cachedScripts      = []string{}
	cachedDpl          = ""
	cachedRequireProof = ""
)

func GetDpl(client httpclient.AuroraHttpClient, proxy string) {
	requestURL := strings.Replace(BaseURL, "/backend-api", "", 1)

	if len(cachedScripts) > 0 {
		return
	}
	if proxy != "" {
		client.SetProxy(proxy)
	}
	header := createBaseHeader()
	response, err := client.Request(http.MethodGet, requestURL, header, nil, nil)

	if err != nil {
		return
	}
	defer response.Body.Close()
	doc, _ := goquery.NewDocumentFromReader(response.Body)
	cachedScripts = nil
	doc.Find("script[src]").Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if exists {
			cachedScripts = append(cachedScripts, src)
			if cachedDpl == "" {
				idx := strings.Index(src, "dpl")
				if idx >= 0 {
					cachedDpl = src[idx:]
				}
			}
		}
	})
	if BasicCookies == nil {
		for _, cookie := range client.GetCookies("https://chatgpt.com") {
			// __Secure-next-auth.callback-url 在登录后服务端会下发,这里强制为根路径
			if cookie.Name == "__Secure-next-auth.callback-url" {
				cookie.Value = "https://chatgpt.com"
			}
			BasicCookies = append(BasicCookies, cookie)
		}
	}
	if len(cachedScripts) == 0 {
		cachedScripts = append(cachedScripts, "https://cdn.oaistatic.com/_next/static/chunks/polyfills-78c92fac7aa8fdd8.js?dpl=baf36960d05dde6d8b941194fa4093fb5cb78c6a")
		cachedDpl = "dpl=baf36960d05dde6d8b941194fa4093fb5cb78c6a"
	}
}

type TurnStile struct {
	TurnStileToken              string
	ProofOfWorkToken            string
	TurnstileToken              string
	ChatRequirementsPrepareToken string // prepare 接口返回的 prepare_token, 在 sentinel/ping 时注入
	ChatRequirementsToken       string // finalize 接口返回的 chat-requirements token (sentinel/ping 复用)
	SentinelReqToken            string // /sentinel/req 返回的 token
	SentinelReqPersona          string // /sentinel/req 返回的 persona
	SOToken                     string
	soSession                   *so.Session
	soSnapshotDX                string
	soChatToken                 string
	soFlow                      string
	soOnce                      sync.Once
	soResult                    string
	soErr                       error
}

type ProofWork struct {
	Difficulty string `json:"difficulty,omitempty"`
	Required   bool   `json:"required"`
	Seed       string `json:"seed,omitempty"`
}

type SoSegment struct {
	Required    bool   `json:"required"`
	CollectorDX string `json:"collector_dx,omitempty"`
	SnapshotDX  string `json:"snapshot_dx,omitempty"`
}

func GetInitConfig() []interface{} {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	script := cachedScripts[rng.Intn(len(cachedScripts))]
	nowMs := float64(time.Now().UnixMilli())
	perfNow := float64(int64(rng.Float64()*49000)+1000) + rng.Float64()
	timeOrigin := nowMs - perfNow
	loc := time.FixedZone("Pacific Standard Time", -8*60*60)
	parseTime := time.Now().In(loc).Format("Mon Jan 02 2006 15:04:05") + " GMT-0800 (Pacific Standard Time)"

	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	reactSuffix := make([]byte, 11)
	for i := range reactSuffix {
		reactSuffix[i] = letters[rng.Intn(len(letters))]
	}

	return []interface{}{
		cachedHardware,     // [0]  screen.width + screen.height
		parseTime,          // [1]  Date.toString()
		int64(4294967296),  // [2]  jsHeapSizeLimit
		rng.Float64(),      // [3]  Math.random()
		defaultUserAgent(), // [4]  navigator.userAgent
		script,             // [5]  currentScript.src
		nil,                // [6]  documentElement[data-build]
		"en-US",            // [7]  navigator.language
		"en-US,en",         // [8]  navigator.languages.join(",")
		rng.Float64(),      // [9]  Math.random()
		"vibrate−function vibrate() { [native code] }", // [10] navigator 原型方法
		"_reactListening" + string(reactSuffix),        // [11] document 随机 key
		"requestIdleCallback",                          // [12] window 随机 key
		perfNow,                                        // [13] performance.now()
		oaiDeviceID,                                    // [14] device_id
		"",                                             // [15] location.search
		16,                                             // [16] hardwareConcurrency (对齐 prooftoken.NewConfig)
		timeOrigin,                                     // [17] performance.timeOrigin
		0, 0, 0, 0, 0, 0, 0,                            // [18-24] "X in window" 检查
	}
}

func CalcProofToken(require *ChatRequire, state *ChatClientState) string {
	ua := defaultUserAgent()
	if state != nil && state.UserAgent != "" {
		ua = state.UserAgent
	}
	return prooftoken.SolveProofToken(require.Proof.Seed, require.Proof.Difficulty, ua)
}

type ChatRequire struct {
	Persona      string    `json:"persona,omitempty"`
	Token        string    `json:"token"`
	PrepareToken string    `json:"prepare_token,omitempty"`
	Proof        ProofWork `json:"proofofwork"`
	Turnstile    struct {
		Required bool   `json:"required"`
		DX       string `json:"dx,omitempty"`
	} `json:"turnstile"`
	So         SoSegment `json:"so"`
	ForceLogin bool      `json:"force_login"`
}

type sentinelFinalizeResponse struct {
	Persona     string `json:"persona,omitempty"`
	Token       string `json:"token"`
	ExpireAfter int    `json:"expire_after,omitempty"`
}

// sentinelExtraData 对齐 chatgpt.com JS 中 rHn() 编码的 OpenAI-Sentinel-Extra-Data header。
// base64(JSON) 格式,携带 conversation ID / 消息 ID 和 token 存在信号。
type sentinelExtraData struct {
	Version        int                 `json:"v"`
	SequenceNumber int                 `json:"sequence_number"`
	Signals        sentinelExtraSignals `json:"signals"`
	ConversationID string              `json:"conversation_id,omitempty"`
	LastMessageID  string              `json:"last_message_id,omitempty"`
}

type sentinelExtraSignals struct {
	PingSource                   string `json:"ping_source"`
	SOTokenPresent               string `json:"so_token_present"`
	TurnstileTokenPresent        string `json:"turnstile_token_present"`
	ProofTokenPresent            string `json:"proof_token_present"`
	PrepareTokenPresent          string `json:"prepare_token_present"`
	ChatRequirementsTokenPresent string `json:"chat_requirements_token_present"`
}

func buildSentinelExtraData(conversationID, lastMessageID string, prepareToken string, chatRequirementsToken string, soTokenPresent bool, turnstileTokenPresent bool, proofTokenPresent bool, pingSource string, sequenceNumber int) string {
	if pingSource == "" {
		pingSource = "session_observer_background_submit"
	}
	signals := sentinelExtraSignals{
		PingSource:                  pingSource,
		SOTokenPresent:              boolToStr(soTokenPresent),
		TurnstileTokenPresent:       boolToStr(turnstileTokenPresent),
		ProofTokenPresent:           boolToStr(proofTokenPresent),
		PrepareTokenPresent:         boolToStr(prepareToken != ""),
		ChatRequirementsTokenPresent: boolToStr(chatRequirementsToken != ""),
	}
	data := sentinelExtraData{
		Version:        1,
		SequenceNumber: sequenceNumber,
		Signals:        signals,
	}
	if conversationID != "" {
		data.ConversationID = "WEB:" + conversationID
	}
	if lastMessageID != "" {
		data.LastMessageID = lastMessageID
	}
	payload, _ := json.Marshal(data)
	return base64.StdEncoding.EncodeToString(payload)
}

func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// pingSentinelResponse 是 POST /backend-api/sentinel/ping 的响应。
type pingSentinelResponse struct {
	Status string `json:"status"`
}

// POSTSentinelPing 调用 /sentinel/ping 端点。
func POSTSentinelPing(client httpclient.AuroraHttpClient, account *accounts.Account, ts *TurnStile, conversationID, lastMessageID string, state *ChatClientState) error {
	return POSTSentinelPingWithSource(client, account, ts, conversationID, lastMessageID, state, "session_observer_background_submit", 0)
}

// POSTSentinelPingWithSource 支持 ping_source 和 sequence_number 自定义。
func POSTSentinelPingWithSource(client httpclient.AuroraHttpClient, account *accounts.Account, ts *TurnStile, conversationID, lastMessageID string, state *ChatClientState, pingSource string, sequenceNumber int) error {
	apiUrl, targetPath := sentinelURL(account, "/sentinel/ping")
	header := sentinelHeaderWithState(account, targetPath, state)
	// 注入所有 sentinel token header
	if ts != nil {
		if ts.ChatRequirementsPrepareToken != "" {
			header.Set("Openai-Sentinel-Chat-Requirements-Prepare-Token", ts.ChatRequirementsPrepareToken)
		}
		if ts.ChatRequirementsToken != "" {
			header.Set("Openai-Sentinel-Chat-Requirements-Token", ts.ChatRequirementsToken)
		} else if ts.TurnStileToken != "" {
			header.Set("Openai-Sentinel-Chat-Requirements-Token", ts.TurnStileToken)
		}
		if ts.TurnstileToken != "" {
			header.Set("Openai-Sentinel-Turnstile-Token", ts.TurnstileToken)
		}
		if ts.ProofOfWorkToken != "" {
			header.Set("Openai-Sentinel-Proof-Token", ts.ProofOfWorkToken)
		}
		if soToken := ts.ensureSOToken(soDeviceIDFor(account)); soToken != "" {
			header.Set("Openai-Sentinel-So-Token", soToken)
		}
		extraData := buildSentinelExtraData(
			conversationID,
			lastMessageID,
			ts.ChatRequirementsPrepareToken,
			ts.ChatRequirementsToken,
			ts.ensureSOToken(soDeviceIDFor(account)) != "",
			ts.TurnstileToken != "",
			ts.ProofOfWorkToken != "",
			pingSource,
			sequenceNumber,
		)
		header.Set("Openai-Sentinel-Extra-Data", extraData)
	}
	response, err := client.Request(http.MethodPost, apiUrl, header, nil, nil)
	if err != nil {
		return fmt.Errorf("sentinel ping failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("sentinel ping failed: %s", readResponseSnippet(response.Body, 500))
	}
	return nil
}

func sentinelURL(account *accounts.Account, path string) (string, string) {
	if account != nil && account.Type != accounts.TypePUID {
		return strings.Replace(BaseURL, "backend-api", "backend-anon", 1) + path, "/backend-anon" + path
	}
	return BaseURL + path, "/backend-api" + path
}

func sentinelHeader(account *accounts.Account, targetPath string) httpclient.AuroraHeaders {
	return sentinelHeaderWithState(account, targetPath, nil)
}

func sentinelHeaderWithState(account *accounts.Account, targetPath string, state *ChatClientState) httpclient.AuroraHeaders {
	header := createBaseHeaderForState(state)
	header.Set("Accept", "*/*")
	header.Set("Content-Type", "application/json")
	header.Set("X-Openai-Target-Path", targetPath)
	header.Set("X-Openai-Target-Route", targetPath)
	if account != nil && account.Type != accounts.TypePUID && account.Token != "" {
		header.Set("Oai-Device-Id", account.Token)
	}
	if account != nil && account.Type == accounts.TypePUID && account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	setTeamAccountHeader(header, account)
	return header
}

func setTeamAccountHeader(header httpclient.AuroraHeaders, account *accounts.Account) {
	if account != nil && strings.TrimSpace(account.TeamUserID) != "" {
		header.Set("Chatgpt-Account-Id", strings.TrimSpace(account.TeamUserID))
	}
}

func conversationURL(account *accounts.Account, path string) (string, string) {
	if account != nil && account.Type != accounts.TypePUID {
		return strings.Replace(BaseURL, "backend-api", "backend-anon", 1) + path, "/backend-anon" + path
	}
	return BaseURL + path, "/backend-api" + path
}

func conversationHeaders(account *accounts.Account, chatToken *TurnStile, accept, targetPath, conduitToken, turnTraceID string) httpclient.AuroraHeaders {
	return conversationHeadersWithState(account, chatToken, accept, targetPath, conduitToken, turnTraceID, nil)
}

func conversationHeadersWithState(account *accounts.Account, chatToken *TurnStile, accept, targetPath, conduitToken, turnTraceID string, state *ChatClientState) httpclient.AuroraHeaders {
	header := createBaseHeaderForState(state)
	header.Set("Accept", accept)
	header.Set("Content-Type", "application/json")
	header.Set("X-Openai-Target-Path", targetPath)
	header.Set("X-Openai-Target-Route", targetPath)
	if turnTraceID != "" {
		header.Set("X-Oai-Turn-Trace-Id", turnTraceID)
	}
	if conduitToken != "" || strings.HasSuffix(targetPath, "/f/conversation") || strings.HasSuffix(targetPath, "/f/conversation/prepare") {
		header.Set("X-Conduit-Token", conduitToken)
	}
	if strings.HasSuffix(targetPath, "/f/conversation") && !strings.HasSuffix(targetPath, "/prepare") {
		header.Set("Oai-Echo-Logs", "0,943,1,65876,0,68124,1,68930")
		header.Set("Oai-Telemetry", "[1,null]")
	}
	if chatToken != nil {
		if chatToken.TurnStileToken != "" {
			header.Set("Openai-Sentinel-Chat-Requirements-Token", chatToken.TurnStileToken)
		}
		if chatToken.ChatRequirementsPrepareToken != "" {
			header.Set("Openai-Sentinel-Chat-Requirements-Prepare-Token", chatToken.ChatRequirementsPrepareToken)
		}
		if chatToken.ProofOfWorkToken != "" {
			header.Set("Openai-Sentinel-Proof-Token", chatToken.ProofOfWorkToken)
		}
		if chatToken.TurnstileToken != "" {
			header.Set("Openai-Sentinel-Turnstile-Token", chatToken.TurnstileToken)
		}
		if soToken := chatToken.ensureSOToken(soDeviceIDFor(account)); soToken != "" {
			header.Set("Openai-Sentinel-So-Token", soToken)
		}
	}
	cookieStr := ""
	if account != nil && account.PUID != "" {
		cookieStr = "_puid=" + account.PUID
	}
	if account != nil && account.Type != accounts.TypePUID && account.Token != "" {
		header.Set("Oai-Device-Id", account.Token)
		if cookieStr != "" {
			cookieStr += "; "
		}
		cookieStr += "oai-did=" + account.Token
	}
	if cookieStr != "" {
		header["Cookie"] = cookieStr
	}
	if account != nil && account.Type == accounts.TypePUID && account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	setTeamAccountHeader(header, account)
	return header
}

func createBaseHeader() httpclient.AuroraHeaders {
	return createBaseHeaderForState(nil)
}

func createBaseHeaderForState(state *ChatClientState) httpclient.AuroraHeaders {
	header := make(httpclient.AuroraHeaders)
	// 对齐 2026-06-24 chatgpt.com 浏览器抓包:Chrome 147 Win64
	header.Set("Accept", "*/*")
	header.Set("Accept-Language", "en-US,en;q=0.9")
	header.Set("Oai-Language", "en-US")
	header.Set("Origin", "https://chatgpt.com")
	// referer 跟 state.ConversationID 联动;空就发首页
	if state != nil && state.ConversationID != "" {
		header.Set("Referer", "https://chatgpt.com/c/"+state.ConversationID)
	} else {
		header.Set("Referer", "https://chatgpt.com/")
	}
	// sec-ch-ua-* 对齐 Chrome 148 (与 UA / prooftoken 同步, 对齐 2026-06-24 浏览器抓包)
	header.Set("Sec-Ch-Ua", `"Chromium";v="148", "Google Chrome";v="148", "Not/A)Brand";v="99"`)
	header.Set("Sec-Ch-Ua-Mobile", "?0")
	header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	header.Set("Priority", "u=1, i")
	header.Set("Sec-Fetch-Dest", "empty")
	header.Set("Sec-Fetch-Mode", "cors")
	header.Set("Sec-Fetch-Site", "same-origin")
	ua := util.FixedUserAgent
	if state != nil && state.UserAgent != "" {
		ua = state.UserAgent
	}
	header.Set("User-Agent", ua)
	deviceID := oaiDeviceID
	sessionID := oaiSessionID
	if state != nil {
		if state.DeviceID != "" {
			deviceID = state.DeviceID
		}
		if state.SessionID != "" {
			sessionID = state.SessionID
		}
	}
	header.Set("Oai-Device-Id", deviceID)
	header.Set("Oai-Session-Id", sessionID)
	// 对齐 2026-06-24 chatgpt.com 浏览器抓包的 build / version
	if fp := browserfp.Get(); fp != nil {
		header.Set("Oai-Client-Version", fp.BuildID)
	} else {
		header.Set("Oai-Client-Version", browserfp.DefaultBuildID)
	}
	header.Set("Oai-Client-Build-Number", "7823760")
	return header
}

// defaultUserAgent 返回全局统一的 User-Agent (Chrome 148 Windows)。
// 一律走 util.FixedUserAgent,不再随机 —
//  1. 网络 header 用途: 防止与 sec-ch-ua-* 失配触发 Cloudflare 风控;
//  2. fingerprint/PoW 用途: 内部算 token 用的 UA 必须跟实际请求一致,
//     随机会让 prooftoken 跟真实 UA 错位导致 sentinel 验证失败。
func defaultUserAgent() string {
	return util.FixedUserAgent
}
