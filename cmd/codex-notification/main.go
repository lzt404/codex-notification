package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	defaultAPIBase       = "https://api.sgroup.qq.com"
	defaultTokenURL      = "https://bots.qq.com/app/getAppAccessToken"
	defaultTelegramBase  = "https://api.telegram.org"
	defaultTimeout       = 30 * time.Second
	defaultCaptureWait   = 10 * time.Minute
	defaultCaptureCmd    = "/openid"
	maxHookInputBytes    = 1024 * 1024
	maxResponseBodyBytes = 1024 * 1024
	maxMessageRunes      = 4000
	msgTypeText          = 0
	gatewayPath          = "/gateway"
	qqGatewayIntentC2C   = 1 << 25
	qqGatewayIntentGuild = 1 << 30
	qqGatewayIntentDM    = 1 << 12
)

type stopHookInput struct {
	SessionID            string  `json:"session_id"`
	TurnID               string  `json:"turn_id"`
	TranscriptPath       *string `json:"transcript_path"`
	CWD                  string  `json:"cwd"`
	HookEventName        string  `json:"hook_event_name"`
	Model                string  `json:"model"`
	ThreadSource         string  `json:"thread_source"`
	AgentRole            string  `json:"agent_role"`
	AgentNickname        string  `json:"agent_nickname"`
	PermissionMode       string  `json:"permission_mode"`
	StopHookActive       bool    `json:"stop_hook_active"`
	LastAssistantMessage *string `json:"last_assistant_message"`
}

type config struct {
	QQ           *qqConfig
	Telegram     *telegramConfig
	Notification notificationConfig
}

type notificationConfig struct {
	AllowedModels  []string
	BlockedModels  []string
	BlockSubagents bool
}

type qqConfig struct {
	AppID        string
	AppSecret    string
	TargetOpenID string
}

type telegramConfig struct {
	BotToken string
	ChatID   string
	APIBase  string
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	Code        int    `json:"code"`
	Message     string `json:"message"`
	Error       string `json:"error"`
}

type sendMessageResponse struct {
	ID      string `json:"id"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

type telegramSendMessageResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

type gatewayResponse struct {
	URL string `json:"url"`
}

type gatewayPayload struct {
	Op int             `json:"op"`
	S  *int64          `json:"s"`
	T  string          `json:"t"`
	D  json.RawMessage `json:"d"`
}

type gatewayHelloData struct {
	HeartbeatInterval int64 `json:"heartbeat_interval"`
}

type captureResult struct {
	TargetOpenID string
	EventType    string
	Content      string
}

type transcriptLine struct {
	Type    string `json:"type"`
	Payload struct {
		ID            string `json:"id"`
		Type          string `json:"type"`
		Role          string `json:"role"`
		ThreadSource  string `json:"thread_source"`
		AgentRole     string `json:"agent_role"`
		AgentNickname string `json:"agent_nickname"`
		Content       []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "capture-openid" || os.Args[1] == "--capture-openid") {
		errCapture := captureOpenID(os.Stdout, os.Stderr, os.Getenv)
		if errCapture != nil {
			_, _ = fmt.Fprintf(os.Stderr, "codex-notification: %v\n", errCapture)
			os.Exit(1)
		}
		return
	}

	errRun := run(os.Stdin, os.Stderr, os.Getenv)
	if errRun == nil {
		return
	}

	_, _ = fmt.Fprintf(os.Stderr, "codex-notification: %v\n", errRun)
}

func run(stdin io.Reader, stderr io.Writer, getenv func(string) string) error {
	cfg, errConfig := loadConfig(getenv)
	if errConfig != nil {
		return errConfig
	}

	input, errInput := readStopHookInput(stdin)
	if errInput != nil {
		return errInput
	}

	if shouldSkipNotification(input, cfg.Notification) {
		return nil
	}

	message := buildNotificationMessage(input)
	message = truncateRunes(strings.TrimSpace(message), maxMessageRunes)
	if message == "" {
		return errors.New("message is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	client := &http.Client{Timeout: defaultTimeout}

	if cfg.QQ != nil {
		token, errToken := fetchAccessToken(ctx, client, *cfg.QQ, stderr)
		if errToken != nil {
			return errToken
		}

		errSend := sendQQTextMessage(ctx, client, *cfg.QQ, token, message, stderr)
		if errSend != nil {
			return errSend
		}
	}

	if cfg.Telegram != nil {
		errSend := sendTelegramTextMessage(ctx, client, *cfg.Telegram, message, stderr)
		if errSend != nil {
			return errSend
		}
	}

	return nil
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		Notification: notificationConfig{
			AllowedModels:  splitCSV(getenv("NOTIFICATION_ALLOWED_MODELS")),
			BlockedModels:  defaultBlockedModels(getenv("NOTIFICATION_BLOCKED_MODELS")),
			BlockSubagents: !envBool(getenv("NOTIFICATION_ALLOW_SUBAGENTS")),
		},
	}

	qq := qqConfig{
		AppID:        strings.TrimSpace(getenv("APP_ID")),
		AppSecret:    strings.TrimSpace(getenv("APP_SECRET")),
		TargetOpenID: strings.TrimSpace(getenv("TARGET_OPENID")),
	}
	if qq.AppID != "" || qq.AppSecret != "" || qq.TargetOpenID != "" {
		if qq.AppID == "" {
			return config{}, errors.New("APP_ID is required")
		}
		if qq.AppSecret == "" {
			return config{}, errors.New("APP_SECRET is required")
		}
		if qq.TargetOpenID == "" {
			return config{}, errors.New("TARGET_OPENID is required")
		}
		cfg.QQ = &qq
	}

	telegram := telegramConfig{
		BotToken: strings.TrimSpace(getenv("TELEGRAM_BOT_TOKEN")),
		ChatID:   strings.TrimSpace(getenv("TELEGRAM_CHAT_ID")),
		APIBase:  strings.TrimRight(strings.TrimSpace(getenv("TELEGRAM_API_BASE")), "/"),
	}
	if telegram.APIBase == "" {
		telegram.APIBase = defaultTelegramBase
	}
	if telegram.BotToken != "" || telegram.ChatID != "" || telegram.APIBase != defaultTelegramBase {
		if telegram.BotToken == "" {
			return config{}, errors.New("TELEGRAM_BOT_TOKEN is required")
		}
		if telegram.ChatID == "" {
			return config{}, errors.New("TELEGRAM_CHAT_ID is required")
		}
		cfg.Telegram = &telegram
	}

	if cfg.QQ == nil && cfg.Telegram == nil {
		return config{}, errors.New("QQ Bot or Telegram Bot configuration is required")
	}

	return cfg, nil
}

func shouldSkipNotification(input stopHookInput, filter notificationConfig) bool {
	if filter.BlockSubagents && isSubagentInput(input) {
		return true
	}

	model := strings.TrimSpace(input.Model)
	if len(filter.AllowedModels) > 0 && !valueMatchesAny(model, filter.AllowedModels) {
		return true
	}
	if valueMatchesAny(model, filter.BlockedModels) {
		return true
	}

	return false
}

func isSubagentInput(input stopHookInput) bool {
	if isSubagentSource(input.ThreadSource) || strings.TrimSpace(input.AgentRole) != "" || strings.TrimSpace(input.AgentNickname) != "" {
		return true
	}
	if input.TranscriptPath == nil {
		return false
	}

	meta := transcriptMeta(*input.TranscriptPath)
	return isSubagentSource(meta.ThreadSource) || strings.TrimSpace(meta.AgentRole) != "" || strings.TrimSpace(meta.AgentNickname) != ""
}

type transcriptMetaResult struct {
	ThreadSource  string
	AgentRole     string
	AgentNickname string
}

func transcriptMeta(transcriptPath string) transcriptMetaResult {
	if strings.TrimSpace(transcriptPath) == "" {
		return transcriptMetaResult{}
	}

	file, errOpen := os.Open(transcriptPath)
	if errOpen != nil {
		return transcriptMetaResult{}
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	if !scanner.Scan() {
		return transcriptMetaResult{}
	}

	var line transcriptLine
	errUnmarshal := json.Unmarshal(scanner.Bytes(), &line)
	if errUnmarshal != nil || line.Type != "session_meta" {
		return transcriptMetaResult{}
	}

	return transcriptMetaResult{
		ThreadSource:  line.Payload.ThreadSource,
		AgentRole:     line.Payload.AgentRole,
		AgentNickname: line.Payload.AgentNickname,
	}
}

func isSubagentSource(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "subagent")
}

func defaultBlockedModels(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{"mini", "small", "lite", "flash", "haiku", "nano"}
	}
	return splitCSV(value)
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}

func valueMatchesAny(value string, patterns []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern != "" && strings.Contains(value, pattern) {
			return true
		}
	}
	return false
}

func envBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func readStopHookInput(stdin io.Reader) (stopHookInput, error) {
	limitedReader := io.LimitReader(stdin, maxHookInputBytes)
	data, errReadAll := io.ReadAll(limitedReader)
	if errReadAll != nil {
		return stopHookInput{}, fmt.Errorf("read hook input: %w", errReadAll)
	}
	if strings.TrimSpace(string(data)) == "" {
		return stopHookInput{}, nil
	}

	var input stopHookInput
	errUnmarshal := json.Unmarshal(data, &input)
	if errUnmarshal != nil {
		return stopHookInput{}, fmt.Errorf("parse hook input JSON: %w", errUnmarshal)
	}

	return input, nil
}

func buildNotificationMessage(input stopHookInput) string {
	var parts []string
	if cwd := strings.TrimSpace(input.CWD); cwd != "" {
		parts = append(parts, "Project directory: "+cwd)
	}
	if taskName := taskNameFromInput(input); taskName != "" {
		parts = append(parts, "Task: "+taskName)
	}
	if finalOutput := finalOutputFromInput(input); finalOutput != "" {
		parts = append(parts, "Final output:\n"+finalOutput)
	}

	return strings.Join(parts, "\n\n")
}

func taskNameFromInput(input stopHookInput) string {
	if input.TranscriptPath != nil {
		if taskName := lastUserMessageFromTranscript(*input.TranscriptPath); taskName != "" {
			return singleLineSummary(taskName, 240)
		}
	}
	if input.LastAssistantMessage != nil {
		return singleLineSummary(*input.LastAssistantMessage, 240)
	}

	return ""
}

func finalOutputFromInput(input stopHookInput) string {
	if input.LastAssistantMessage == nil {
		return ""
	}

	return strings.TrimSpace(*input.LastAssistantMessage)
}

func lastUserMessageFromTranscript(transcriptPath string) string {
	if strings.TrimSpace(transcriptPath) == "" {
		return ""
	}

	file, errOpen := os.Open(transcriptPath)
	if errOpen != nil {
		return ""
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	lastUserMessage := ""
	for scanner.Scan() {
		var line transcriptLine
		if errUnmarshal := json.Unmarshal(scanner.Bytes(), &line); errUnmarshal != nil {
			continue
		}
		if line.Type != "response_item" || line.Payload.Type != "message" || line.Payload.Role != "user" {
			continue
		}
		text := transcriptContentText(line.Payload.Content)
		if strings.TrimSpace(text) != "" {
			lastUserMessage = text
		}
	}

	return strings.TrimSpace(lastUserMessage)
}

func transcriptContentText(content []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var parts []string
	for _, item := range content {
		if strings.TrimSpace(item.Text) != "" {
			parts = append(parts, strings.TrimSpace(item.Text))
		}
	}

	return strings.Join(parts, "\n")
}

func singleLineSummary(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	return truncateRunes(value, limit)
}

func fetchAccessToken(ctx context.Context, client *http.Client, cfg qqConfig, stderr io.Writer) (string, error) {
	requestBody := map[string]string{
		"appId":        cfg.AppID,
		"clientSecret": cfg.AppSecret,
	}
	responseBody, errRequest := postJSON(ctx, client, defaultTokenURL, "", requestBody, stderr)
	if errRequest != nil {
		return "", fmt.Errorf("get QQ Bot access token: %w", errRequest)
	}

	var token tokenResponse
	errUnmarshal := json.Unmarshal(responseBody, &token)
	if errUnmarshal != nil {
		return "", fmt.Errorf("parse QQ Bot token response: %w", errUnmarshal)
	}
	if token.AccessToken == "" {
		return "", fmt.Errorf("QQ Bot token response missing access_token: %s", string(responseBody))
	}

	return token.AccessToken, nil
}

func sendQQTextMessage(ctx context.Context, client *http.Client, cfg qqConfig, accessToken string, content string, stderr io.Writer) error {
	endpointPath := "/v2/users/" + url.PathEscape(cfg.TargetOpenID) + "/messages"
	requestBody := map[string]any{
		"content":  content,
		"msg_type": msgTypeText,
		"msg_seq":  nextMsgSeq(),
	}

	responseBody, errRequest := postJSON(ctx, client, defaultAPIBase+endpointPath, "QQBot "+accessToken, requestBody, stderr)
	if errRequest != nil {
		return fmt.Errorf("send QQ Bot message: %w", errRequest)
	}

	if len(strings.TrimSpace(string(responseBody))) == 0 {
		return nil
	}

	var response sendMessageResponse
	errUnmarshal := json.Unmarshal(responseBody, &response)
	if errUnmarshal != nil {
		return nil
	}
	if response.Code != 0 {
		detail := firstNonEmpty(response.Message, response.Error)
		return fmt.Errorf("QQ Bot message API returned code %d: %s", response.Code, detail)
	}

	return nil
}

func sendTelegramTextMessage(ctx context.Context, client *http.Client, cfg telegramConfig, content string, stderr io.Writer) error {
	endpoint := cfg.APIBase + "/bot" + cfg.BotToken + "/sendMessage"
	requestBody := map[string]any{
		"chat_id": cfg.ChatID,
		"text":    content,
	}

	responseBody, errRequest := postJSON(ctx, client, endpoint, "", requestBody, stderr)
	if errRequest != nil {
		return fmt.Errorf("send Telegram Bot message: %w", errRequest)
	}

	var response telegramSendMessageResponse
	errUnmarshal := json.Unmarshal(responseBody, &response)
	if errUnmarshal != nil {
		return fmt.Errorf("parse Telegram Bot message response: %w", errUnmarshal)
	}
	if !response.OK {
		return fmt.Errorf("Telegram Bot message API returned error %d: %s", response.ErrorCode, response.Description)
	}

	return nil
}

func captureOpenID(stdout io.Writer, stderr io.Writer, getenv func(string) string) error {
	cfg, errConfig := loadCaptureConfig(getenv)
	if errConfig != nil {
		return errConfig
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultCaptureWait)
	defer cancel()

	client := &http.Client{Timeout: defaultTimeout}
	token, errToken := fetchAccessToken(ctx, client, cfg, stderr)
	if errToken != nil {
		return errToken
	}

	gatewayURL, errGateway := fetchGatewayURL(ctx, client, token, stderr)
	if errGateway != nil {
		return errGateway
	}

	_, _ = fmt.Fprintf(stderr, "codex-notification: waiting for QQ message command %q for up to %s\n", defaultCaptureCmd, defaultCaptureWait)
	_, _ = fmt.Fprintln(stderr, "codex-notification: send this command to the bot from the QQ account you want to notify")

	conn, _, errDial := websocket.Dial(ctx, gatewayURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"User-Agent": []string{"codex-notification/0.1"},
		},
	})
	if errDial != nil {
		return fmt.Errorf("connect QQ Bot gateway: %w", errDial)
	}
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "done")
	}()

	var writeMu sync.Mutex
	var latestSeq atomic.Int64
	latestSeq.Store(-1)
	var heartbeatCancel context.CancelFunc
	defer func() {
		if heartbeatCancel != nil {
			heartbeatCancel()
		}
	}()

	for {
		var payload gatewayPayload
		errRead := wsjson.Read(ctx, conn, &payload)
		if errRead != nil {
			return fmt.Errorf("read QQ Bot gateway event: %w", errRead)
		}
		if payload.S != nil {
			latestSeq.Store(*payload.S)
		}

		switch payload.Op {
		case 10:
			interval := heartbeatInterval(payload.D)
			if heartbeatCancel != nil {
				heartbeatCancel()
			}
			heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
			heartbeatCancel = cancelHeartbeat
			go heartbeatLoop(heartbeatCtx, conn, &writeMu, &latestSeq, interval, stderr)

			errIdentify := sendGatewayIdentify(ctx, conn, &writeMu, token)
			if errIdentify != nil {
				return errIdentify
			}
		case 0:
			if payload.T == "READY" {
				_, _ = fmt.Fprintln(stderr, "codex-notification: QQ Bot gateway ready")
				continue
			}

			result, ok := captureOpenIDFromPayload(payload)
			if !ok {
				continue
			}
			printCaptureResult(stdout, result)
			return nil
		case 11:
			continue
		}
	}
}

func loadCaptureConfig(getenv func(string) string) (qqConfig, error) {
	cfg := qqConfig{
		AppID:     strings.TrimSpace(getenv("APP_ID")),
		AppSecret: strings.TrimSpace(getenv("APP_SECRET")),
	}
	if cfg.AppID == "" {
		return qqConfig{}, errors.New("APP_ID is required")
	}
	if cfg.AppSecret == "" {
		return qqConfig{}, errors.New("APP_SECRET is required")
	}

	return cfg, nil
}

func fetchGatewayURL(ctx context.Context, client *http.Client, accessToken string, stderr io.Writer) (string, error) {
	responseBody, errRequest := getJSON(ctx, client, defaultAPIBase+gatewayPath, "QQBot "+accessToken, stderr)
	if errRequest != nil {
		return "", fmt.Errorf("get QQ Bot gateway URL: %w", errRequest)
	}

	var response gatewayResponse
	errUnmarshal := json.Unmarshal(responseBody, &response)
	if errUnmarshal != nil {
		return "", fmt.Errorf("parse QQ Bot gateway response: %w", errUnmarshal)
	}
	if response.URL == "" {
		return "", fmt.Errorf("QQ Bot gateway response missing url: %s", string(responseBody))
	}

	return response.URL, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, authorization string, stderr io.Writer) ([]byte, error) {
	req, errNewRequest := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if errNewRequest != nil {
		return nil, fmt.Errorf("create HTTP request: %w", errNewRequest)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-notification/0.1")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, errDo
	}
	defer func() {
		errClose := resp.Body.Close()
		if errClose != nil {
			_, _ = fmt.Fprintf(stderr, "codex-notification: response body close error: %v\n", errClose)
		}
	}()

	responseBody, errReadAll := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if errReadAll != nil {
		return nil, fmt.Errorf("read HTTP response: %w", errReadAll)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	return responseBody, nil
}

func heartbeatInterval(raw json.RawMessage) time.Duration {
	var hello gatewayHelloData
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &hello)
	}
	if hello.HeartbeatInterval <= 0 {
		hello.HeartbeatInterval = 30000
	}
	return time.Duration(float64(hello.HeartbeatInterval)*0.8) * time.Millisecond
}

func heartbeatLoop(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, latestSeq *atomic.Int64, interval time.Duration, stderr io.Writer) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var seq any
			if value := latestSeq.Load(); value >= 0 {
				seq = value
			}
			payload := map[string]any{"op": 1, "d": seq}
			errWrite := writeGatewayJSON(ctx, conn, writeMu, payload)
			if errWrite != nil {
				_, _ = fmt.Fprintf(stderr, "codex-notification: heartbeat failed: %v\n", errWrite)
				return
			}
		}
	}
}

func sendGatewayIdentify(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, accessToken string) error {
	payload := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":   "QQBot " + accessToken,
			"intents": qqGatewayIntentC2C | qqGatewayIntentGuild | qqGatewayIntentDM,
			"shard":   []int{0, 1},
			"properties": map[string]string{
				"$os":      "macOS",
				"$browser": "codex-notification",
				"$device":  "codex-notification",
			},
		},
	}
	return writeGatewayJSON(ctx, conn, writeMu, payload)
}

func writeGatewayJSON(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, payload any) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	return wsjson.Write(ctx, conn, payload)
}

func captureOpenIDFromPayload(payload gatewayPayload) (captureResult, bool) {
	if payload.T != "C2C_MESSAGE_CREATE" {
		return captureResult{}, false
	}

	var data map[string]any
	errUnmarshal := json.Unmarshal(payload.D, &data)
	if errUnmarshal != nil {
		return captureResult{}, false
	}

	content := strings.TrimSpace(stringFromMap(data, "content"))
	if !captureCommandMatches(content) {
		return captureResult{}, false
	}

	author := mapFromMap(data, "author")
	result := captureResult{
		TargetOpenID: stringFromMap(author, "user_openid"),
		EventType:    payload.T,
		Content:      content,
	}

	return result, result.TargetOpenID != ""
}

func captureCommandMatches(content string) bool {
	return content == defaultCaptureCmd || strings.Contains(content, defaultCaptureCmd)
}

func printCaptureResult(stdout io.Writer, result captureResult) {
	_, _ = fmt.Fprintf(stdout, "# Captured from QQ Bot event: %s\n", result.EventType)
	_, _ = fmt.Fprintf(stdout, "TARGET_OPENID=%s\n", result.TargetOpenID)
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func mapFromMap(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	value, ok := values[key]
	if !ok {
		return nil
	}
	typed, _ := value.(map[string]any)
	return typed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, authorization string, requestPayload any, stderr io.Writer) ([]byte, error) {
	body, errMarshal := json.Marshal(requestPayload)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal JSON request: %w", errMarshal)
	}

	req, errNewRequest := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if errNewRequest != nil {
		return nil, fmt.Errorf("create HTTP request: %w", errNewRequest)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-notification/0.1")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, errDo
	}
	defer func() {
		errClose := resp.Body.Close()
		if errClose != nil {
			_, _ = fmt.Fprintf(stderr, "codex-notification: response body close error: %v\n", errClose)
		}
	}()

	responseBody, errReadAll := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if errReadAll != nil {
		return nil, fmt.Errorf("read HTTP response: %w", errReadAll)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	return responseBody, nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func nextMsgSeq() int64 {
	return time.Now().UnixNano()%1000000000 + 1
}
