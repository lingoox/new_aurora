package chatgpt

import (
	"aurora/httpclient"
	"aurora/internal/browserfp"
	"aurora/internal/fingerprint"
	"aurora/internal/prooftoken"
	"aurora/internal/so"
	"aurora/internal/tokens"
	"aurora/internal/turnstile"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func InitTurnStileWithState(client httpclient.AuroraHttpClient, secret *tokens.Secret, proxy string, state *ChatClientState) (*TurnStile, int, error) {
	return InitSentinelWithState(client, secret, proxy, 0, state)
}

func InitSentinel(client httpclient.AuroraHttpClient, secret *tokens.Secret, proxy string, retry int) (*TurnStile, int, error) {
	return InitSentinelWithState(client, secret, proxy, retry, nil)
}

func InitSentinelWithState(client httpclient.AuroraHttpClient, secret *tokens.Secret, proxy string, retry int, state *ChatClientState) (*TurnStile, int, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	ua := defaultUserAgent()
	if state != nil && state.UserAgent != "" {
		ua = state.UserAgent
	}
	requirementsToken := prooftoken.NewConfig(ua).RequirementsToken()

	prepare, status, err := POSTSentinelPrepareWithState(client, secret, requirementsToken, state)
	if err != nil {
		if secret.IsFree && status == http.StatusUnauthorized && retry < 2 {
			time.Sleep(time.Second * 2)
			secret.Token = uuid.NewString()
			return InitSentinelWithState(client, secret, proxy, retry+1, state)
		}
		return nil, status, err
	}
	if prepare.ForceLogin {
		if !secret.IsFree {
			return nil, http.StatusUnauthorized, fmt.Errorf("force login required: ChatGPT access token is expired or not accepted")
		}
		if retry > 1 {
			return nil, http.StatusForbidden, fmt.Errorf("force login required")
		}
		time.Sleep(time.Second)
		secret.Token = uuid.NewString()
		return InitSentinelWithState(client, secret, proxy, retry+1, state)
	}
	if prepare.PrepareToken == "" {
		return nil, status, fmt.Errorf("sentinel prepare token is missing")
	}

	var proofToken string
	if prepare.Proof.Required {
		proofToken = CalcProofToken(prepare, state)
		if proofToken == "" {
			return nil, http.StatusForbidden, errors.New("calculation proof token failure. Please retry the operation")
		}
	}
	var turnstileToken string
	if prepare.Turnstile.DX != "" {
		turnstileToken, _ = turnstile.SolveDX(requirementsToken, prepare.Turnstile.DX)
		if turnstileToken == "" {
			turnstileToken, _ = turnstile.SolveDX(requirementsToken, prepare.Turnstile.DX)
		}
	}

	// 构建 TurnStile (先于 finalize)
	ts := &TurnStile{
		ProofOfWorkToken:             proofToken,
		TurnstileToken:               turnstileToken,
		ChatRequirementsPrepareToken: prepare.PrepareToken,
	}

	// so 段
	if prepare.So.Required && prepare.So.CollectorDX != "" && prepare.So.SnapshotDX != "" && prepare.Token != "" {
		ts.soSession = so.NewSession(requirementsToken, prepare.So.CollectorDX)
		ts.soSnapshotDX = prepare.So.SnapshotDX
		ts.soChatToken = prepare.Token
		ts.soFlow = stateFlow(state, ua)
		ts.soSession.Start()
	}

	finalize, status, err := POSTSentinelFinalizeWithState(client, secret, prepare.PrepareToken, proofToken, turnstileToken, state)
	if err != nil {
		if secret.IsFree && status == http.StatusUnauthorized && retry < 2 {
			time.Sleep(time.Second * 2)
			secret.Token = uuid.NewString()
			return InitSentinelWithState(client, secret, proxy, retry+1, state)
		}
		return nil, status, err
	}
	if finalize.Token == "" {
		return nil, status, fmt.Errorf("sentinel finalize token is missing")
	}

	ts.TurnStileToken = finalize.Token
	ts.ChatRequirementsToken = finalize.Token

	return ts, status, nil
}

// stateFlow 推导 so token 里的 flow 字段(对齐 deob_js/out.js:924 ce() 行为)。
// 优先用 secret.Token 当作 flow 标识;若 secret 不可用则用 ua 简写。
func stateFlow(state *ChatClientState, ua string) string {
	if state != nil && state.DeviceID != "" {
		return state.DeviceID
	}
	if ua != "" {
		return "chatgpt-freeaccount"
	}
	return "chatgpt"
}

// soDeviceIDFor 给出 openai-sentinel-so-token 的 deviceID 参数。对齐 out.js
// sessionObserverToken() 流程,deviceID 是 ne.get() 的 key,也是 ce({...}, t) 里的
// id;实际取值对应 qn.getCookies()["oai-did"](out.js:735),即 secret.Token。
func soDeviceIDFor(secret *tokens.Secret) string {
	if secret != nil && secret.Token != "" {
		return secret.Token
	}
	return ""
}

// ensureSOToken 懒求值 openai-sentinel-so-token header 值:第一次调用时跑
// snapshot_dx(复用 collector 留下的 VM 寄存器),后续直接返回缓存结果。
// 对齐 out.js sessionObserverToken():取 snapshot 后用 ce({so,c}, id, flow) 编码。
// deviceID 是这次请求使用的实际 deviceID(通常来自 secret.Token 或 cookie)。
func (ts *TurnStile) ensureSOToken(deviceID string) string {
	if ts == nil || ts.soSession == nil {
		return ts.SOToken
	}
	ts.soOnce.Do(func() {
		soResult, err := ts.soSession.Snapshot(ts.soSnapshotDX)
		if err != nil {
			ts.soErr = err
			return
		}
		ts.soResult = soResult
	})
	if ts.soErr != nil {
		return ""
	}
	if ts.SOToken != "" {
		return ts.SOToken
	}
	tok, err := so.BuildToken(ts.soResult, ts.soChatToken, deviceID, ts.soFlow)
	if err != nil {
		return ""
	}
	ts.SOToken = tok
	return ts.SOToken
}

func POSTSentinelPrepare(client httpclient.AuroraHttpClient, secret *tokens.Secret, requirementsToken string) (*ChatRequire, int, error) {
	return POSTSentinelPrepareWithState(client, secret, requirementsToken, nil)
}

func POSTSentinelPrepareWithState(client httpclient.AuroraHttpClient, secret *tokens.Secret, requirementsToken string, state *ChatClientState) (*ChatRequire, int, error) {
	apiUrl, targetPath := sentinelURL(secret, "/sentinel/chat-requirements/prepare")
	bodyJSON, err := json.Marshal(map[string]string{"p": requirementsToken})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	header := sentinelHeaderWithState(secret, targetPath, state)
	response, err := client.Request(http.MethodPost, apiUrl, header, nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, fmt.Errorf("sentinel prepare failed: %s", readResponseSnippet(response.Body, 500))
	}
	var result ChatRequire
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, response.StatusCode, err
	}
	return &result, response.StatusCode, nil
}

func POSTSentinelFinalize(client httpclient.AuroraHttpClient, secret *tokens.Secret, prepareToken, proofToken, turnstileToken string) (*sentinelFinalizeResponse, int, error) {
	return POSTSentinelFinalizeWithState(client, secret, prepareToken, proofToken, turnstileToken, nil)
}

func POSTSentinelFinalizeWithState(client httpclient.AuroraHttpClient, secret *tokens.Secret, prepareToken, proofToken, turnstileToken string, state *ChatClientState) (*sentinelFinalizeResponse, int, error) {
	apiUrl, targetPath := sentinelURL(secret, "/sentinel/chat-requirements/finalize")
	payload := map[string]string{"prepare_token": prepareToken}
	if proofToken != "" {
		payload["proofofwork"] = proofToken
	}
	if turnstileToken != "" {
		payload["turnstile"] = turnstileToken
	}
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	header := sentinelHeaderWithState(secret, targetPath, state)
	response, err := client.Request(http.MethodPost, apiUrl, header, nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, fmt.Errorf("sentinel finalize failed: %s", readResponseSnippet(response.Body, 500))
	}
	var result sentinelFinalizeResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, response.StatusCode, err
	}
	return &result, response.StatusCode, nil
}

// conversationInitResponse 是 POST /conversation/init 的响应。
// 对齐浏览器 2026-06 chatgpt.com 抓包。
type conversationInitResponse struct {
	Type              string `json:"type"`
	BannerInfo        any    `json:"banner_info"`
	DefaultModelSlug  string `json:"default_model_slug"`
	AtlasModeEnabled  any    `json:"atlas_mode_enabled"`
}

// POSTConversationInit 调用 /conversation/init 端点 — 对齐浏览器行为:
// 在 sentinel 流程完成后调用,获取对话元数据(default_model_slug, limits 等)。
// 浏览器在页面加载时调用此 API 以建立会话上下文。
func POSTConversationInit(client httpclient.AuroraHttpClient, secret *tokens.Secret, state *ChatClientState) (*conversationInitResponse, error) {
	// free 用户走 backend-anon,paid 走 backend-api
	var apiUrl string
	if secret != nil && secret.IsFree {
		apiUrl = strings.Replace(BaseURL, "backend-api", "backend-anon", 1) + "/conversation/init"
	} else {
		apiUrl = BaseURL + "/conversation/init"
	}
	targetPath := "/backend-api/conversation/init"
	header := createBaseHeaderForState(state)
	header.Set("Accept", "*/*")
	header.Set("Content-Type", "application/json")
	header.Set("X-Openai-Target-Path", targetPath)
	header.Set("X-Openai-Target-Route", targetPath)
	if secret != nil && secret.IsFree && secret.Token != "" {
		header.Set("Oai-Device-Id", secret.Token)
	}
	if secret != nil && !secret.IsFree && secret.Token != "" {
		header.Set("Authorization", "Bearer "+secret.Token)
	}
	setTeamAccountHeader(header, secret)
	payload := map[string]any{
		"requested_default_model": nil,
		"conversation_id":         nil,
		"timezone_offset_min":     -480,
		"conversation_origin":     nil,
	}
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	response, err := client.Request(http.MethodPost, apiUrl, header, nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("conversation init failed: %s", readResponseSnippet(response.Body, 500))
	}
	var result conversationInitResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// sentinelReqResponse 是 POST /sentinel/req 的响应。
// 服务端会返回 token + flow 字段(对齐 sdk.deob.pretty.js / OpenSentinel client.js)。
type sentinelReqResponse struct {
	Token     string `json:"token"`
	Flow      string `json:"flow"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	ChatReq   string `json:"chat_req,omitempty"` // 备用:有时服务端把 chat-requirements token 嵌在这里
	Persona   string `json:"persona,omitempty"`
}

// buildSentinelReqToken 为 /sentinel/req 端点生成指纹 token。
//
// 对齐 2026-06-24 浏览器抓包: /sentinel/req 使用与 prepare **完全相同** 的
// 25 元素 Build25 格式,唯一区别是 [3] nonce=2 (prepare=1)。
// 直接复用 fingerprint.Build25(),不手写重复数组。
func buildSentinelReqToken(state *ChatClientState) string {
	ua := defaultUserAgent()
	deviceID := oaiDeviceID
	if state != nil {
		if state.UserAgent != "" {
			ua = state.UserAgent
		}
		if state.DeviceID != "" {
			deviceID = state.DeviceID
		}
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	fp := browserfp.Get()

	opts := fingerprint.Options{
		UserAgent:           ua,
		ScreenWidth:         fp.ScreenWidth,
		ScreenHeight:        fp.ScreenHeight,
		HardwareConcurrency: fp.HardwareConcurrency,
		JSHeapSizeLimit:     fp.JSHeapSizeLimit,
		BuildID:             fp.BuildID,
		Languages:           strings.Split(browserfp.LanguageJoin(fp.Language), ","),
		Rand:                rng,
	}

	config := fingerprint.Build25(opts)
	config[3] = 2      // nonce: req 用 2 (prepare 用 1)
	config[14] = deviceID

	encoded := prooftoken.EncodeConfig(config)
	return "gAAAAAC" + encoded + "~S"
}

// randomReactSuffix 生成类似 React container suffix 的随机字符串。
func randomReactSuffix() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, 11)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}

// randomWindowKey 返回随机 window 属性名。
func randomWindowKey() string {
	keys := []string{"onseeking", "onfocus", "onblur", "requestIdleCallback", "webkitRequestAnimationFrame", "__oai_so_bc", "__oai_so_ly"}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return keys[rng.Intn(len(keys))]
}

// POSTSentinelReq 调用 /sentinel/req 端点 (对齐 2026-06-24 浏览器抓包)。
//
// /sentinel/req 使用与 /chat-requirements/prepare **相同** 的 25 元素指纹格式,
// 仅 [3] nonce 不同 (prepare=1, req=2)。
func POSTSentinelReq(client httpclient.AuroraHttpClient, secret *tokens.Secret, requirementsToken, deviceID, flow string, state *ChatClientState) (*sentinelReqResponse, int, error) {
	if flow == "" {
		flow = "conversation"
	}
	// 使用与 prepare 相同的指纹格式,但 nonce=2
	reqToken := buildSentinelReqToken(state)
	apiUrl, targetPath := sentinelURL(secret, "/sentinel/req")
	bodyJSON, err := json.Marshal(map[string]string{
		"p":    reqToken,
		"id":   deviceID,
		"flow": flow,
	})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	header := createBaseHeaderForState(state)
	header.Set("Accept", "*/*")
	// 对齐 conversation.txt:sentinel/req 端点用 text/plain;charset=UTF-8
	header.Set("Content-Type", "text/plain;charset=UTF-8")
	header.Set("X-Openai-Target-Path", targetPath)
	header.Set("X-Openai-Target-Route", targetPath)
	// referer 应该指向 sentinel/frame.html(对齐 conversation.txt 抓包)
	if state == nil || state.ConversationID == "" {
		header.Set("Referer", "https://chatgpt.com/backend-api/sentinel/frame.html?sv=20260423af3c")
	}
	if secret != nil && secret.IsFree && secret.Token != "" {
		header.Set("Oai-Device-Id", secret.Token)
	}
	if secret != nil && !secret.IsFree && secret.Token != "" {
		header.Set("Authorization", "Bearer "+secret.Token)
	}
	setTeamAccountHeader(header, secret)
	response, err := client.Request(http.MethodPost, apiUrl, header, nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, fmt.Errorf("sentinel req failed: %s", readResponseSnippet(response.Body, 500))
	}
	var result sentinelReqResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, response.StatusCode, err
	}
	return &result, response.StatusCode, nil
}

func readResponseSnippet(body io.Reader, limit int64) string {
	if limit <= 0 {
		limit = 500
	}
	data, err := io.ReadAll(io.LimitReader(body, limit))
	if err != nil {
		return err.Error()
	}
	return string(data)
}
