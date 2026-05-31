package main

import (
	"bufio"
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	defaultAPIBase       = "https://api.sgroup.qq.com"
	defaultTokenURL      = "https://bots.qq.com/app/getAppAccessToken"
	defaultTelegramBase  = "https://api.telegram.org"
	defaultWeChatAPIBase = "https://ilinkai.weixin.qq.com"
	defaultWeChatAppID   = "bot"
	defaultWeChatVersion = "1"
	defaultWeChatBotType = "3"
	defaultWeChatChannel = "1.0.0"
	defaultTimeout       = 30 * time.Second
	defaultWeChatTimeout = 45 * time.Second
	defaultWeChatPoll    = 1200 * time.Millisecond
	maxWeChatPoll        = 5 * time.Second
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
	weChatTextItemType   = 1
	weChatMessageBot     = 2
	weChatMessageFinish  = 2
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
	WeChat       *weChatConfig
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

type weChatConfig struct {
	APIBase            string
	Token              string
	AccountID          string
	TargetUserID       string
	ContextToken       string
	IlinkAppID         string
	IlinkClientVersion string
}

type weChatLoginConfig struct {
	APIBase            string
	BotType            string
	IlinkAppID         string
	IlinkClientVersion string
}

type weChatAPIResponse struct {
	Ret     int    `json:"ret"`
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

type weChatQRResponse struct {
	QRCode        string `json:"qrcode"`
	QRCodeContent string `json:"qrcode_img_content"`
	QRCodeURL     string `json:"qrcode_url"`
	weChatAPIResponse
}

type weChatLoginStatusResponse struct {
	Status       string `json:"status"`
	RedirectHost string `json:"redirect_host"`
	BotToken     string `json:"bot_token"`
	BotID        string `json:"ilink_bot_id"`
	UserID       string `json:"ilink_user_id"`
	BaseURL      string `json:"baseurl"`
	weChatAPIResponse
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

type weChatSendMessageResponse struct{ weChatAPIResponse }

type httpStatusError struct {
	StatusCode int
	Body       string
}

func (err httpStatusError) Error() string {
	body := strings.TrimSpace(err.Body)
	if body == "" {
		return fmt.Sprintf("HTTP %d", err.StatusCode)
	}
	return fmt.Sprintf("HTTP %d: %s", err.StatusCode, body)
}

type weChatUpdatesResponse struct {
	Messages             []weChatUpdateMessage `json:"msgs"`
	GetUpdatesBuf        string                `json:"get_updates_buf"`
	LongPollingTimeoutMS int64                 `json:"long_polling_timeout"`
	weChatAPIResponse
}

type weChatUpdateMessage struct {
	FromUserID   string              `json:"from_user_id"`
	ToUserID     string              `json:"to_user_id"`
	ContextToken string              `json:"context_token"`
	ItemList     []weChatMessageItem `json:"item_list"`
}

type weChatSendMessageRequest struct {
	Message  weChatOutboundMessage `json:"msg"`
	BaseInfo weChatBaseInfo        `json:"base_info"`
}

type weChatOutboundMessage struct {
	FromUserID   string              `json:"from_user_id"`
	ToUserID     string              `json:"to_user_id"`
	ClientID     string              `json:"client_id"`
	MessageType  int                 `json:"message_type"`
	MessageState int                 `json:"message_state"`
	ContextToken string              `json:"context_token"`
	ItemList     []weChatMessageItem `json:"item_list"`
}

type weChatMessageItem struct {
	Type     int            `json:"type"`
	TextItem weChatTextItem `json:"text_item"`
}

type weChatTextItem struct {
	Text string `json:"text"`
}

type weChatUpdatesRequest struct {
	GetUpdatesBuf string         `json:"get_updates_buf"`
	BaseInfo      weChatBaseInfo `json:"base_info"`
}

type weChatBaseInfo struct {
	ChannelVersion string `json:"channel_version"`
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
	getenv := getenvWithEnvFile(os.Getenv)

	if len(os.Args) > 1 && (os.Args[1] == "capture-openid" || os.Args[1] == "--capture-openid") {
		errCapture := captureOpenID(os.Stdout, os.Stderr, getenv)
		if errCapture != nil {
			_, _ = fmt.Fprintf(os.Stderr, "codex-notification: %v\n", errCapture)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "capture-wechat" || os.Args[1] == "--capture-wechat" || os.Args[1] == "wechat-login") {
		errCapture := captureWeChat(os.Stdout, os.Stderr, getenv)
		if errCapture != nil {
			_, _ = fmt.Fprintf(os.Stderr, "codex-notification: %v\n", errCapture)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "capture-wechat-context" || os.Args[1] == "--capture-wechat-context" || os.Args[1] == "wechat-context") {
		errCapture := captureWeChatContext(os.Stdout, os.Stderr, getenv)
		if errCapture != nil {
			_, _ = fmt.Fprintf(os.Stderr, "codex-notification: %v\n", errCapture)
			os.Exit(1)
		}
		return
	}
	errRun := run(os.Stdin, os.Stderr, getenv)
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

	var sendErrors []error
	if cfg.Telegram != nil {
		errSend := sendTelegramTextMessage(ctx, client, *cfg.Telegram, message, stderr)
		if errSend != nil {
			sendErrors = append(sendErrors, errSend)
		}
	}

	if cfg.WeChat != nil {
		errSend := sendWeChatTextMessage(ctx, client, *cfg.WeChat, message, stderr)
		if errSend != nil {
			sendErrors = append(sendErrors, errSend)
		}
	}

	return errors.Join(sendErrors...)
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

	wechat, ok, errWeChat := loadWeChatConfig(getenv)
	if errWeChat != nil {
		return config{}, errWeChat
	}
	if ok {
		cfg.WeChat = &wechat
	}

	if cfg.QQ == nil && cfg.Telegram == nil && cfg.WeChat == nil {
		return config{}, errors.New("QQ Bot, Telegram Bot, or WeChat configuration is required")
	}

	return cfg, nil
}

func loadWeChatConfig(getenv func(string) string) (weChatConfig, bool, error) {
	explicitAPIBase := strings.TrimRight(strings.TrimSpace(getenv("WECHAT_API_BASE")), "/")
	explicitToken := strings.TrimSpace(getenv("WECHAT_TOKEN"))
	explicitAccountID := strings.TrimSpace(getenv("WECHAT_ACCOUNT_ID"))
	explicitTargetUserID := strings.TrimSpace(getenv("WECHAT_TARGET_USER_ID"))
	explicitContextToken := strings.TrimSpace(getenv("WECHAT_CONTEXT_TOKEN"))
	enabled := envBool(getenv("WECHAT_ENABLED")) ||
		explicitAPIBase != "" ||
		explicitToken != "" ||
		explicitAccountID != "" ||
		explicitTargetUserID != "" ||
		explicitContextToken != ""
	if !enabled {
		return weChatConfig{}, false, nil
	}

	cfg := weChatConfig{
		APIBase:            explicitAPIBase,
		Token:              explicitToken,
		AccountID:          explicitAccountID,
		TargetUserID:       explicitTargetUserID,
		ContextToken:       explicitContextToken,
		IlinkAppID:         strings.TrimSpace(getenv("WECHAT_ILINK_APP_ID")),
		IlinkClientVersion: strings.TrimSpace(getenv("WECHAT_ILINK_APP_CLIENT_VERSION")),
	}
	if cfg.IlinkAppID == "" {
		cfg.IlinkAppID = defaultWeChatAppID
	}
	if cfg.IlinkClientVersion == "" {
		cfg.IlinkClientVersion = defaultWeChatVersion
	}

	if cfg.APIBase == "" {
		cfg.APIBase = defaultWeChatAPIBase
	}
	if cfg.Token == "" {
		return weChatConfig{}, true, errors.New("WECHAT_TOKEN is required")
	}
	if cfg.AccountID == "" {
		return weChatConfig{}, true, errors.New("WECHAT_ACCOUNT_ID is required")
	}
	if cfg.TargetUserID == "" {
		return weChatConfig{}, true, errors.New("WECHAT_TARGET_USER_ID is required")
	}

	return cfg, true, nil
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

type envAssignment struct {
	Name  string
	Value string
}

func getenvWithEnvFile(getenv func(string) string) func(string) string {
	envPath, errPath := codexNotificationEnvPath(getenv)
	if errPath != nil {
		return getenv
	}
	values, errValues := readEnvFileValues(envPath)
	if errValues != nil {
		return getenv
	}

	return func(name string) string {
		if value := getenv(name); value != "" {
			return value
		}
		return values[name]
	}
}

func readEnvFileValues(path string) (map[string]string, error) {
	content, errRead := os.ReadFile(path)
	if errRead != nil {
		if errors.Is(errRead, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, errRead
	}

	values := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		name, value, ok := parseEnvLine(line)
		if ok {
			values[name] = value
		}
	}
	return values, nil
}

func parseEnvLine(line string) (string, string, bool) {
	trimmedLine := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
		return "", "", false
	}
	separatorIndex := strings.Index(trimmedLine, "=")
	if separatorIndex <= 0 {
		return "", "", false
	}

	name := strings.TrimSpace(trimmedLine[:separatorIndex])
	if !isEnvName(name) {
		return "", "", false
	}
	value := convertEnvValue(trimmedLine[separatorIndex+1:])
	return name, value, true
}

func convertEnvValue(value string) string {
	trimmedValue := strings.TrimSpace(value)
	if len(trimmedValue) >= 2 {
		firstChar := trimmedValue[:1]
		lastChar := trimmedValue[len(trimmedValue)-1:]
		if (firstChar == `"` && lastChar == `"`) || (firstChar == `'` && lastChar == `'`) {
			return trimmedValue[1 : len(trimmedValue)-1]
		}
	}
	return trimmedValue
}

func isEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func codexNotificationEnvPath(getenv func(string) string) (string, error) {
	if envPath := strings.TrimSpace(getenv("CODEX_NOTIFICATION_ENV")); envPath != "" {
		return envPath, nil
	}

	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		home = strings.TrimSpace(getenv("USERPROFILE"))
	}
	if home == "" {
		var errHome error
		home, errHome = os.UserHomeDir()
		if errHome != nil {
			return "", fmt.Errorf("resolve home directory: %w", errHome)
		}
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("resolve home directory: HOME or USERPROFILE is required")
	}

	return filepath.Join(home, ".codex", "codex-notification.env"), nil
}

func saveEnvValues(getenv func(string) string, assignments []envAssignment) (string, error) {
	envPath, errPath := codexNotificationEnvPath(getenv)
	if errPath != nil {
		return "", errPath
	}

	content, mode, errRead := readEnvFileForUpdate(envPath)
	if errRead != nil {
		return "", errRead
	}

	updatedContent := mergeEnvContent(content, assignments)
	if errMkdir := os.MkdirAll(filepath.Dir(envPath), 0o700); errMkdir != nil {
		return "", fmt.Errorf("create env directory: %w", errMkdir)
	}
	if errWrite := os.WriteFile(envPath, []byte(updatedContent), mode); errWrite != nil {
		return "", fmt.Errorf("write env file: %w", errWrite)
	}

	return envPath, nil
}

func readEnvFileForUpdate(path string) (string, os.FileMode, error) {
	info, errStat := os.Stat(path)
	if errStat != nil {
		if errors.Is(errStat, os.ErrNotExist) {
			return "", 0o600, nil
		}
		return "", 0, fmt.Errorf("stat env file: %w", errStat)
	}
	content, errRead := os.ReadFile(path)
	if errRead != nil {
		return "", 0, fmt.Errorf("read env file: %w", errRead)
	}
	return string(content), info.Mode().Perm(), nil
}

func mergeEnvContent(content string, assignments []envAssignment) string {
	values := make(map[string]string)
	order := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		name := strings.TrimSpace(assignment.Name)
		if !isEnvName(name) {
			continue
		}
		if _, ok := values[name]; !ok {
			order = append(order, name)
		}
		values[name] = assignment.Value
	}

	var lines []string
	if content != "" {
		lines = strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	seen := make(map[string]bool)
	for index, line := range lines {
		name, _, ok := parseEnvLine(line)
		if !ok {
			continue
		}
		value, replace := values[name]
		if !replace {
			continue
		}
		lines[index] = name + "=" + value
		seen[name] = true
	}

	for _, name := range order {
		if !seen[name] {
			lines = append(lines, name+"="+values[name])
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
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

func sendWeChatTextMessage(ctx context.Context, client *http.Client, cfg weChatConfig, content string, stderr io.Writer) error {
	if strings.TrimSpace(cfg.ContextToken) == "" {
		return errors.New("WECHAT_CONTEXT_TOKEN is required for WeChat message delivery; run capture-wechat-context and send a message to the WeChat bot")
	}

	requestBody := weChatSendMessageRequest{
		Message: weChatOutboundMessage{
			FromUserID:   "",
			ToUserID:     cfg.TargetUserID,
			ClientID:     nextWeChatClientID(),
			MessageType:  weChatMessageBot,
			MessageState: weChatMessageFinish,
			ContextToken: cfg.ContextToken,
			ItemList: []weChatMessageItem{
				{
					Type: weChatTextItemType,
					TextItem: weChatTextItem{
						Text: content,
					},
				},
			},
		},
		BaseInfo: newWeChatBaseInfo(),
	}

	responseBody, errRequest := postWeChatJSON(ctx, client, cfg, "ilink/bot/sendmessage", requestBody, stderr)
	if errRequest != nil {
		return fmt.Errorf("send WeChat message: %w", errRequest)
	}

	var response weChatSendMessageResponse
	errUnmarshal := json.Unmarshal(responseBody, &response)
	if errUnmarshal != nil {
		return fmt.Errorf("parse WeChat message response: %w", errUnmarshal)
	}
	if errValidate := validateWeChatAPIResponse("WeChat message API", response.weChatAPIResponse); errValidate != nil {
		return errValidate
	}

	return nil
}

func captureWeChat(stdout io.Writer, stderr io.Writer, getenv func(string) string) error {
	cfg := loadWeChatLoginConfig(getenv)

	ctx, cancel := context.WithTimeout(context.Background(), defaultCaptureWait)
	defer cancel()

	client := &http.Client{Timeout: defaultTimeout}
	authBaseURL := cfg.APIBase

	for ctx.Err() == nil {
		qr, errQR := getWeChatLoginQR(ctx, client, cfg, authBaseURL, stdout)
		if errQR != nil {
			if retryWeChatPoll(ctx, stdout, "get WeChat login QR", errQR) {
				continue
			}
			return errQR
		}

		qrKey := strings.TrimSpace(qr.QRCode)
		qrContent := terminalWeChatQRContent(qr)
		if qrKey == "" || qrContent == "" {
			return errors.New("WeChat QR response missing terminal-printable QR content")
		}

		_, _ = fmt.Fprintln(stdout, "codex-notification: scan this WeChat QR code and confirm login on your phone")
		printTerminalQRCode(stdout, qrContent)

		for ctx.Err() == nil {
			status, errStatus := pollWeChatLoginStatus(ctx, client, cfg, authBaseURL, qrKey, stdout)
			if errStatus != nil {
				if retryWeChatPoll(ctx, stdout, "poll WeChat login status", errStatus) {
					continue
				}
				return errStatus
			}

			switch status.Status {
			case "scaned":
				_, _ = fmt.Fprintln(stdout, "codex-notification: QR scanned, waiting for phone confirmation")
				sleep(ctx, defaultWeChatPoll)
			case "scaned_but_redirect":
				if strings.TrimSpace(status.RedirectHost) != "" {
					authBaseURL = "https://" + strings.TrimSpace(status.RedirectHost)
				}
				sleep(ctx, defaultWeChatPoll)
			case "confirmed":
				confirmedConfig, errConfirmed := weChatConfigFromLoginStatus(status, firstNonEmpty(status.BaseURL, authBaseURL), cfg)
				if errConfirmed != nil {
					return errConfirmed
				}

				contextToken, errContext := waitWeChatContextToken(ctx, &http.Client{Timeout: defaultWeChatTimeout}, confirmedConfig, stdout)
				if errContext != nil {
					return fmt.Errorf("capture WeChat context token: %w", errContext)
				}
				confirmedConfig.ContextToken = contextToken
				envPath, errSave := saveEnvValues(getenv, weChatConfigEnvAssignments(confirmedConfig))
				if errSave != nil {
					return fmt.Errorf("save WeChat configuration: %w", errSave)
				}
				_, _ = fmt.Fprintf(stdout, "codex-notification: saved WeChat configuration to %s\n", envPath)
				return nil
			case "expired":
				_, _ = fmt.Fprintln(stdout, "codex-notification: QR expired, requesting a new one")
				goto nextQR
			default:
				sleep(ctx, defaultWeChatPoll)
			}
		}

	nextQR:
	}

	return ctx.Err()
}

func loadWeChatLoginConfig(getenv func(string) string) weChatLoginConfig {
	cfg := weChatLoginConfig{
		APIBase:            strings.TrimRight(strings.TrimSpace(getenv("WECHAT_API_BASE")), "/"),
		BotType:            strings.TrimSpace(getenv("WECHAT_BOT_TYPE")),
		IlinkAppID:         strings.TrimSpace(getenv("WECHAT_ILINK_APP_ID")),
		IlinkClientVersion: strings.TrimSpace(getenv("WECHAT_ILINK_APP_CLIENT_VERSION")),
	}
	if cfg.APIBase == "" {
		cfg.APIBase = defaultWeChatAPIBase
	}
	if cfg.BotType == "" {
		cfg.BotType = defaultWeChatBotType
	}
	if cfg.IlinkAppID == "" {
		cfg.IlinkAppID = defaultWeChatAppID
	}
	if cfg.IlinkClientVersion == "" {
		cfg.IlinkClientVersion = defaultWeChatVersion
	}
	return cfg
}

func weChatConfigFromLoginStatus(status weChatLoginStatusResponse, apiBase string, loginCfg weChatLoginConfig) (weChatConfig, error) {
	if strings.TrimSpace(status.BotToken) == "" {
		return weChatConfig{}, errors.New("WeChat login confirmed but bot_token is empty")
	}
	if strings.TrimSpace(status.BotID) == "" {
		return weChatConfig{}, errors.New("WeChat login confirmed but ilink_bot_id is empty")
	}
	if strings.TrimSpace(status.UserID) == "" {
		return weChatConfig{}, errors.New("WeChat login confirmed but ilink_user_id is empty")
	}

	return weChatConfig{
		APIBase:            strings.TrimRight(strings.TrimSpace(apiBase), "/"),
		Token:              strings.TrimSpace(status.BotToken),
		AccountID:          strings.TrimSpace(status.BotID),
		TargetUserID:       strings.TrimSpace(status.UserID),
		IlinkAppID:         loginCfg.IlinkAppID,
		IlinkClientVersion: loginCfg.IlinkClientVersion,
	}, nil
}

func captureWeChatContext(stdout io.Writer, stderr io.Writer, getenv func(string) string) error {
	cfg, ok, errConfig := loadWeChatConfig(getenv)
	if errConfig != nil {
		return errConfig
	}
	if !ok {
		return errors.New("WeChat configuration is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultCaptureWait)
	defer cancel()

	client := &http.Client{Timeout: defaultWeChatTimeout}
	contextToken, errToken := waitWeChatContextToken(ctx, client, cfg, stdout)
	if errToken != nil {
		return errToken
	}
	envPath, errSave := saveEnvValues(getenv, []envAssignment{{Name: "WECHAT_CONTEXT_TOKEN", Value: strings.TrimSpace(contextToken)}})
	if errSave != nil {
		return fmt.Errorf("save WeChat context token: %w", errSave)
	}
	_, _ = fmt.Fprintf(stdout, "codex-notification: saved WeChat context token to %s\n", envPath)
	return nil
}

func waitWeChatContextToken(ctx context.Context, client *http.Client, cfg weChatConfig, stderr io.Writer) (string, error) {
	_, _ = fmt.Fprintln(stderr, "codex-notification: send any message to the WeChat bot now to capture WECHAT_CONTEXT_TOKEN")

	updatesBuf := ""
	for ctx.Err() == nil {
		updates, errUpdates := getWeChatUpdates(ctx, client, cfg, updatesBuf, stderr)
		if errUpdates != nil {
			if retryWeChatPoll(ctx, stderr, "get WeChat updates", errUpdates) {
				continue
			}
			return "", errUpdates
		}
		if strings.TrimSpace(updates.GetUpdatesBuf) != "" {
			updatesBuf = strings.TrimSpace(updates.GetUpdatesBuf)
		}

		for _, message := range updates.Messages {
			if !matchesWeChatContextMessage(cfg, message) {
				continue
			}
			return strings.TrimSpace(message.ContextToken), nil
		}

		sleep(ctx, weChatUpdatesPollInterval(updates.LongPollingTimeoutMS))
	}

	return "", ctx.Err()
}

func matchesWeChatContextMessage(cfg weChatConfig, message weChatUpdateMessage) bool {
	if strings.TrimSpace(message.ContextToken) == "" {
		return false
	}
	if strings.TrimSpace(cfg.TargetUserID) != "" && strings.TrimSpace(message.FromUserID) != "" && strings.TrimSpace(message.FromUserID) != strings.TrimSpace(cfg.TargetUserID) {
		return false
	}
	if strings.TrimSpace(cfg.AccountID) != "" && strings.TrimSpace(message.ToUserID) != "" && strings.TrimSpace(message.ToUserID) != strings.TrimSpace(cfg.AccountID) {
		return false
	}

	return true
}

func weChatUpdatesPollInterval(timeoutMS int64) time.Duration {
	wait := time.Duration(timeoutMS) * time.Millisecond
	if wait <= 0 || wait > maxWeChatPoll {
		return defaultWeChatPoll
	}
	return wait
}

func getWeChatUpdates(ctx context.Context, client *http.Client, cfg weChatConfig, updatesBuf string, stderr io.Writer) (weChatUpdatesResponse, error) {
	requestBody := weChatUpdatesRequest{
		GetUpdatesBuf: updatesBuf,
		BaseInfo:      newWeChatBaseInfo(),
	}
	responseBody, errRequest := postWeChatJSON(ctx, client, cfg, "ilink/bot/getupdates", requestBody, stderr)
	if errRequest != nil {
		return weChatUpdatesResponse{}, fmt.Errorf("get WeChat updates: %w", errRequest)
	}

	var response weChatUpdatesResponse
	errUnmarshal := json.Unmarshal(responseBody, &response)
	if errUnmarshal != nil {
		return weChatUpdatesResponse{}, fmt.Errorf("parse WeChat updates response: %w", errUnmarshal)
	}
	if errValidate := validateWeChatAPIResponse("WeChat updates API", response.weChatAPIResponse); errValidate != nil {
		return weChatUpdatesResponse{}, errValidate
	}

	return response, nil
}

func getWeChatLoginQR(ctx context.Context, client *http.Client, cfg weChatLoginConfig, baseURL string, stderr io.Writer) (weChatQRResponse, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/ilink/bot/get_bot_qrcode?bot_type=" + url.QueryEscape(cfg.BotType)
	responseBody, errRequest := getJSONWithHeaders(ctx, client, endpoint, map[string]string{
		"Accept":     "application/json",
		"User-Agent": "codex-notification/0.1",
	}, stderr)
	if errRequest != nil {
		return weChatQRResponse{}, fmt.Errorf("get WeChat login QR: %w", errRequest)
	}

	var response weChatQRResponse
	errUnmarshal := json.Unmarshal(responseBody, &response)
	if errUnmarshal != nil {
		return weChatQRResponse{}, fmt.Errorf("parse WeChat login QR response: %w", errUnmarshal)
	}
	if errValidate := validateWeChatAPIResponse("WeChat login QR API", response.weChatAPIResponse); errValidate != nil {
		return weChatQRResponse{}, errValidate
	}
	return response, nil
}

func pollWeChatLoginStatus(ctx context.Context, client *http.Client, cfg weChatLoginConfig, baseURL string, qrKey string, stderr io.Writer) (weChatLoginStatusResponse, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(qrKey)
	headers := map[string]string{
		"Accept":                  "application/json",
		"User-Agent":              "codex-notification/0.1",
		"iLink-App-Id":            cfg.IlinkAppID,
		"iLink-App-ClientVersion": cfg.IlinkClientVersion,
	}
	responseBody, errRequest := getJSONWithHeaders(ctx, client, endpoint, headers, stderr)
	if errRequest != nil {
		return weChatLoginStatusResponse{}, fmt.Errorf("poll WeChat login status: %w", errRequest)
	}

	var response weChatLoginStatusResponse
	errUnmarshal := json.Unmarshal(responseBody, &response)
	if errUnmarshal != nil {
		return weChatLoginStatusResponse{}, fmt.Errorf("parse WeChat login status response: %w", errUnmarshal)
	}
	if errValidate := validateWeChatAPIResponse("WeChat login status API", response.weChatAPIResponse); errValidate != nil {
		return weChatLoginStatusResponse{}, errValidate
	}
	return response, nil
}

func validateWeChatAPIResponse(apiName string, response weChatAPIResponse) error {
	if response.Ret != 0 {
		detail := firstNonEmpty(response.ErrMsg, response.Message, response.Error)
		return fmt.Errorf("%s returned ret %d: %s", apiName, response.Ret, detail)
	}
	if response.ErrCode != 0 {
		detail := firstNonEmpty(response.ErrMsg, response.Message, response.Error)
		return fmt.Errorf("%s returned errcode %d: %s", apiName, response.ErrCode, detail)
	}

	return nil
}

func weChatConfigEnvAssignments(cfg weChatConfig) []envAssignment {
	assignments := []envAssignment{
		{Name: "WECHAT_TOKEN", Value: strings.TrimSpace(cfg.Token)},
		{Name: "WECHAT_ACCOUNT_ID", Value: strings.TrimSpace(cfg.AccountID)},
		{Name: "WECHAT_TARGET_USER_ID", Value: strings.TrimSpace(cfg.TargetUserID)},
	}
	if strings.TrimSpace(cfg.ContextToken) != "" {
		assignments = append(assignments, envAssignment{Name: "WECHAT_CONTEXT_TOKEN", Value: strings.TrimSpace(cfg.ContextToken)})
	}
	assignments = append(assignments, envAssignment{Name: "WECHAT_API_BASE", Value: strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/")})
	return assignments
}

func newWeChatBaseInfo() weChatBaseInfo {
	return weChatBaseInfo{
		ChannelVersion: defaultWeChatChannel,
	}
}

func terminalWeChatQRContent(qr weChatQRResponse) string {
	for _, content := range []string{qr.QRCodeURL, qr.QRCodeContent} {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		if _, ok := decodeImageContent(content); ok {
			continue
		}
		return content
	}
	content := strings.TrimSpace(qr.QRCode)
	if content != "" && !isWeChatQRKey(content) {
		if _, ok := decodeImageContent(content); !ok {
			return content
		}
	}
	return ""
}

func isWeChatQRKey(content string) bool {
	if len(content) != 32 {
		return false
	}
	for _, char := range content {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') {
			continue
		}
		return false
	}
	return true
}

func retryWeChatPoll(ctx context.Context, stderr io.Writer, action string, err error) bool {
	if !isRetryableWeChatRequestError(ctx, err) {
		return false
	}
	_, _ = fmt.Fprintf(stderr, "codex-notification: %s failed, retrying: %v\n", action, err)
	sleep(ctx, defaultWeChatPoll)
	return true
}

func isRetryableWeChatRequestError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var statusErr httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusRequestTimeout ||
			statusErr.StatusCode == http.StatusTooManyRequests ||
			statusErr.StatusCode >= http.StatusInternalServerError
	}
	return false
}

func printTerminalQRCode(w io.Writer, content string) {
	qr, errEncode := qrcode.New(content, qrcode.Medium)
	if errEncode != nil {
		_, _ = fmt.Fprintln(w, content)
		return
	}
	_, _ = fmt.Fprintln(w, qr.ToSmallString(false))
}

func decodeImageContent(content string) ([]byte, bool) {
	trimmed := strings.TrimSpace(content)
	if comma := strings.Index(trimmed, ","); strings.HasPrefix(trimmed, "data:image/") && comma >= 0 {
		trimmed = trimmed[comma+1:]
	}

	data, errDecode := base64.StdEncoding.DecodeString(trimmed)
	if errDecode != nil || len(data) < 8 {
		return nil, false
	}
	if bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) || bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) {
		return data, true
	}
	return nil, false
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

	_, _ = fmt.Fprintf(stdout, "codex-notification: waiting for QQ message command %q for up to %s\n", defaultCaptureCmd, defaultCaptureWait)
	_, _ = fmt.Fprintln(stdout, "codex-notification: send this command to the bot from the QQ account you want to notify")

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
				_, _ = fmt.Fprintln(stdout, "codex-notification: QQ Bot gateway ready")
				continue
			}

			result, ok := captureOpenIDFromPayload(payload)
			if !ok {
				continue
			}
			envPath, errSave := saveEnvValues(getenv, []envAssignment{{Name: "TARGET_OPENID", Value: result.TargetOpenID}})
			if errSave != nil {
				return fmt.Errorf("save QQ Bot TARGET_OPENID: %w", errSave)
			}
			_, _ = fmt.Fprintf(stdout, "codex-notification: captured TARGET_OPENID from QQ Bot event %s\n", result.EventType)
			_, _ = fmt.Fprintf(stdout, "codex-notification: saved QQ Bot TARGET_OPENID to %s\n", envPath)
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
	headers := map[string]string{
		"Accept":     "application/json",
		"User-Agent": "codex-notification/0.1",
	}
	if authorization != "" {
		headers["Authorization"] = authorization
	}
	return getJSONWithHeaders(ctx, client, endpoint, headers, stderr)
}

func getJSONWithHeaders(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, stderr io.Writer) ([]byte, error) {
	req, errNewRequest := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if errNewRequest != nil {
		return nil, fmt.Errorf("create HTTP request: %w", errNewRequest)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
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
		return nil, httpStatusError{StatusCode: resp.StatusCode, Body: string(responseBody)}
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
	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"User-Agent":   "codex-notification/0.1",
	}
	if authorization != "" {
		headers["Authorization"] = authorization
	}
	return postJSONWithHeaders(ctx, client, endpoint, headers, requestPayload, stderr)
}

func postJSONWithHeaders(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, requestPayload any, stderr io.Writer) ([]byte, error) {
	body, errMarshal := json.Marshal(requestPayload)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal JSON request: %w", errMarshal)
	}

	req, errNewRequest := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if errNewRequest != nil {
		return nil, fmt.Errorf("create HTTP request: %w", errNewRequest)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
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
		return nil, httpStatusError{StatusCode: resp.StatusCode, Body: string(responseBody)}
	}

	return responseBody, nil
}

func postWeChatJSON(ctx context.Context, client *http.Client, cfg weChatConfig, endpointPath string, requestPayload any, stderr io.Writer) ([]byte, error) {
	headers := map[string]string{
		"Content-Type":      "application/json",
		"Accept":            "application/json",
		"User-Agent":        "codex-notification/0.1",
		"AuthorizationType": "ilink_bot_token",
		"Authorization":     "Bearer " + cfg.Token,
		"X-WECHAT-UIN":      randomWeChatUIN(),
	}
	if cfg.IlinkAppID != "" {
		headers["iLink-App-Id"] = cfg.IlinkAppID
	}
	if cfg.IlinkClientVersion != "" {
		headers["iLink-App-ClientVersion"] = cfg.IlinkClientVersion
	}
	endpoint := strings.TrimRight(cfg.APIBase, "/") + "/" + strings.TrimLeft(endpointPath, "/")
	return postJSONWithHeaders(ctx, client, endpoint, headers, requestPayload, stderr)
}

func randomWeChatUIN() string {
	var randomBytes [4]byte
	_, errRead := cryptorand.Read(randomBytes[:])
	if errRead != nil {
		value := uint32(time.Now().UnixNano())
		return base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(value), 10)))
	}

	value := binary.BigEndian.Uint32(randomBytes[:])
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(value), 10)))
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

func nextWeChatClientID() string {
	return "codex-notification-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func sleep(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
