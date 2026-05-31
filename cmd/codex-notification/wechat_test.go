package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendWeChatTextMessagePayload(t *testing.T) {
	var gotRequest weChatSendMessageRequest
	var gotPath string
	var gotAuthorization string
	var gotAuthorizationType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotAuthorizationType = r.Header.Get("AuthorizationType")

		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ret":0,"errcode":0}`)
	}))
	defer server.Close()

	cfg := weChatConfig{
		APIBase:      server.URL,
		Token:        "token-value",
		TargetUserID: "target-user",
		ContextToken: "context-token",
	}
	err := sendWeChatTextMessage(context.Background(), server.Client(), cfg, "hello 微信", io.Discard)
	if err != nil {
		t.Fatalf("send WeChat message: %v", err)
	}

	if gotPath != "/ilink/bot/sendmessage" {
		t.Fatalf("path = %q, want /ilink/bot/sendmessage", gotPath)
	}
	if gotAuthorization != "Bearer token-value" {
		t.Fatalf("Authorization = %q", gotAuthorization)
	}
	if gotAuthorizationType != "ilink_bot_token" {
		t.Fatalf("AuthorizationType = %q", gotAuthorizationType)
	}
	if gotRequest.BaseInfo.ChannelVersion != defaultWeChatChannel {
		t.Fatalf("channel version = %q", gotRequest.BaseInfo.ChannelVersion)
	}
	if gotRequest.Message.FromUserID != "" {
		t.Fatalf("from_user_id = %q, want empty", gotRequest.Message.FromUserID)
	}
	if gotRequest.Message.ToUserID != "target-user" {
		t.Fatalf("to_user_id = %q", gotRequest.Message.ToUserID)
	}
	if gotRequest.Message.ContextToken != "context-token" {
		t.Fatalf("context_token = %q", gotRequest.Message.ContextToken)
	}
	if len(gotRequest.Message.ItemList) != 1 {
		t.Fatalf("item count = %d", len(gotRequest.Message.ItemList))
	}
	if gotRequest.Message.ItemList[0].TextItem.Text != "hello 微信" {
		t.Fatalf("text = %q", gotRequest.Message.ItemList[0].TextItem.Text)
	}
}

func TestSendWeChatTextMessageRequiresContextToken(t *testing.T) {
	err := sendWeChatTextMessage(context.Background(), http.DefaultClient, weChatConfig{}, "hello", io.Discard)
	if err == nil {
		t.Fatal("expected missing context token error")
	}
	if !strings.Contains(err.Error(), "WECHAT_CONTEXT_TOKEN") {
		t.Fatalf("error = %q, want WECHAT_CONTEXT_TOKEN", err.Error())
	}
}

func TestWaitWeChatContextToken(t *testing.T) {
	var gotRequest weChatUpdatesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/getupdates" {
			t.Fatalf("path = %q, want /ilink/bot/getupdates", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ret":0,"errcode":0,"msgs":[{"from_user_id":"target-user","to_user_id":"account-id","context_token":"captured-context"}],"get_updates_buf":"next"}`)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	token, err := waitWeChatContextToken(ctx, server.Client(), weChatConfig{
		APIBase:      server.URL,
		Token:        "token-value",
		AccountID:    "account-id",
		TargetUserID: "target-user",
	}, io.Discard)
	if err != nil {
		t.Fatalf("wait context token: %v", err)
	}
	if token != "captured-context" {
		t.Fatalf("token = %q", token)
	}
	if gotRequest.BaseInfo.ChannelVersion != defaultWeChatChannel {
		t.Fatalf("channel version = %q", gotRequest.BaseInfo.ChannelVersion)
	}
}
