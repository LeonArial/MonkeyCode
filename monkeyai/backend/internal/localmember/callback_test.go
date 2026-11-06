package localmember

import (
	"context"
	"encoding/json"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/internal/identity"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type authSettings struct{ value json.RawMessage }

func (s authSettings) GetValue(context.Context, string) (json.RawMessage, error) { return s.value, nil }

func TestOIDCCallback(t *testing.T) {
	w, admin := fixture(t)
	var issuer string
	email := "admin@example.com"
	upstream := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(rw).Encode(map[string]string{"authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "userinfo_endpoint": issuer + "/userinfo"})
		case "/token":
			if r.FormValue("client_id") != "client" || r.FormValue("client_secret") != "secret" || r.FormValue("code") != "valid-code" {
				t.Error("令牌请求参数无效")
				rw.WriteHeader(400)
				return
			}
			json.NewEncoder(rw).Encode(map[string]string{"access_token": "token"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Error("用户信息请求缺少认证")
			}
			json.NewEncoder(rw).Encode(map[string]any{"sub": email, "email": email, "email_verified": true, "name": "OIDC用户"})
		default:
			http.NotFound(rw, r)
		}
	}))
	defer upstream.Close()
	issuer = upstream.URL
	config, _ := json.Marshal(map[string]any{"oauth_connections": []identity.OAuthConnection{{ID: "custom", Provider: "oidc", IssuerURL: issuer, ClientID: "client", ClientSecret: "secret", Enabled: true}}})
	s := identity.NewService(w.pool, authSettings{config}, "http://local.example").WithUserWriter(w)
	for _, isAdmin := range []bool{true, false} {
		path := "/oauth/custom/admin-start"
		if !isAdmin {
			email = "registered@example.com"
			values := url.Values{"client_id": {"monkeyai-desktop"}, "redirect_uri": {"monkeyai-desktop://oauth/callback"}, "response_type": {"code"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "state": {"client-state"}}
			request, err := s.BeginAuthorization(t.Context(), values)
			if err != nil {
				t.Fatal(err)
			}
			path = "/oauth/custom/start?request_id=" + request.ID
		}
		start := httptest.NewRecorder()
		s.AuthRouter().ServeHTTP(start, httptest.NewRequest("GET", path, nil))
		if start.Code != 302 {
			t.Fatalf("发起登录: %d %s", start.Code, start.Body.String())
		}
		target, err := url.Parse(start.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if target.Query().Get("client_id") != "client" || target.Query().Get("redirect_uri") != "http://local.example/api/auth/v1/oauth/callback" {
			t.Fatal("授权参数丢失")
		}
		callbackPath := "/oauth/callback?code=valid-code&state=" + url.QueryEscape(target.Query().Get("state"))
		callback := httptest.NewRecorder()
		s.AuthRouter().ServeHTTP(callback, httptest.NewRequest("GET", callbackPath, nil))
		if callback.Code != 302 || len(callback.Result().Cookies()) != 1 || strings.Contains(callback.Header().Get("Location"), "error") {
			t.Fatalf("回调失败: %d %s", callback.Code, callback.Header())
		}
		session := httptest.NewRequest("GET", "/session", nil)
		session.AddCookie(callback.Result().Cookies()[0])
		result := httptest.NewRecorder()
		s.AuthRouter().ServeHTTP(result, session)
		if result.Code != 200 || !strings.Contains(result.Body.String(), email) {
			t.Fatalf("登录会话失败: %s", result.Body.String())
		}
		if isAdmin && !strings.Contains(result.Body.String(), admin) {
			t.Fatal("未关联已有管理员")
		}
		replay := httptest.NewRecorder()
		s.AuthRouter().ServeHTTP(replay, httptest.NewRequest("GET", callbackPath, nil))
		if len(replay.Result().Cookies()) != 0 {
			t.Fatal("回调 state 可被重放")
		}
	}
}
