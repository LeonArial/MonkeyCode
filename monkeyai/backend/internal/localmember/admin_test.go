package localmember

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chaitin/MonkeyCode/monkeyai/backend/internal/group"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/internal/identity"
	"github.com/go-chi/chi/v5"
)

func TestAdminMemberAPI(t *testing.T) {
	w, _ := fixture(t)
	s := identity.NewService(w.pool, authSettings{json.RawMessage(`{}`)}, "http://local.example").WithUserWriter(w)
	router := chi.NewRouter()
	router.Mount("/auth", s.AuthRouter())
	admin := chi.NewRouter()
	admin.Use(s.RequireAdmin)
	s.RegisterAdmin(admin)
	group.NewService(w.pool).WithMemberWriter(Groups{}).RegisterAdmin(admin)
	router.Mount("/admin", admin)
	call := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	login := call("POST", "/auth/admin/login", `{"email":"admin@example.com","password":"initial-password-123"}`, nil)
	if login.Code != 200 {
		t.Fatalf("管理员登录: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	addedGroup := call("POST", "/admin/groups", `{"name":"测试部门"}`, cookie)
	if addedGroup.Code != 201 {
		t.Fatalf("新增分组: %d %s", addedGroup.Code, addedGroup.Body.String())
	}
	var g struct {
		ID              string `json:"id"`
		InitialPassword string `json:"initial_password"`
	}
	json.Unmarshal(addedGroup.Body.Bytes(), &g)
	added := call("POST", "/admin/users", `{"name":"新成员","email":"member@example.com","role":"user","password":"member-password","group_ids":["`+g.ID+`"]}`, cookie)
	if added.Code != 201 {
		t.Fatalf("新增成员: %d %s", added.Code, added.Body.String())
	}
	var u struct {
		ID              string `json:"id"`
		InitialPassword string `json:"initial_password"`
	}
	json.Unmarshal(added.Body.Bytes(), &u)
	duplicate := call("POST", "/admin/users", `{"name":"重复成员","email":"member@example.com","role":"user"}`, cookie)
	if duplicate.Code != 409 {
		t.Fatalf("重复邮箱: %d", duplicate.Code)
	}
	initialBody := `{"email":"member@example.com","password":"` + u.InitialPassword + `"}`
	memberLogin := call("POST", "/auth/login", initialBody, nil)
	if memberLogin.Code != 200 {
		t.Fatalf("成员登录: %d %s", memberLogin.Code, memberLogin.Body.String())
	}
	memberCookie := memberLogin.Result().Cookies()[0]
	if denied := call("POST", "/admin/users", `{}`, memberCookie); denied.Code != 403 {
		t.Fatalf("普通成员越权: %d", denied.Code)
	}
	reset := call("POST", "/admin/users/"+u.ID+"/reset-password", `{}`, cookie)
	if reset.Code != 200 {
		t.Fatalf("重置密码: %d %s", reset.Code, reset.Body.String())
	}
	if old := call("POST", "/auth/login", initialBody, nil); old.Code != 401 {
		t.Fatal("旧密码仍然有效")
	}
	var password struct {
		Password string `json:"password"`
	}
	json.Unmarshal(reset.Body.Bytes(), &password)
	if updated := call("POST", "/auth/login", `{"email":"member@example.com","password":"`+password.Password+`"}`, nil); updated.Code != 200 {
		t.Fatal("新密码无法登录")
	}
	if changed := call("PATCH", "/admin/users/"+u.ID, `{"name":"已停用","role":"user","status":"disabled"}`, cookie); changed.Code != 200 {
		t.Fatalf("停用成员: %d %s", changed.Code, changed.Body.String())
	}
	if blocked := call("POST", "/auth/login", `{"email":"member@example.com","password":"`+password.Password+`"}`, nil); blocked.Code != 401 {
		t.Fatal("停用成员仍能登录")
	}
	if assigned := call("PUT", "/admin/groups/"+g.ID+"/members", `{"member_ids":[]}`, cookie); assigned.Code != 200 {
		t.Fatalf("分组成员调整: %s", assigned.Body.String())
	}
	if deleted := call("DELETE", "/admin/groups/"+g.ID, ``, cookie); deleted.Code != 204 {
		t.Fatalf("删除分组: %s", deleted.Body.String())
	}
}
