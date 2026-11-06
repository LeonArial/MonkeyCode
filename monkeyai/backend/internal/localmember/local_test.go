package localmember

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"crypto/rand"
	"fmt"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/internal/identity/sqlc"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/member"
	"github.com/jackc/pgx/v5/pgxpool"
)

func fixture(t *testing.T) (*Writer, string) {
	t.Helper()
	dsn := os.Getenv("MONKEYAI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("需要独立测试数据库")
	}
	ctx := t.Context()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("localmember_%x", rand.Text())
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(); root.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); root.Close() })
	files, _ := filepath.Glob("../../migrations/*.up.sql")
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(b)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	w := New(pool)
	if err := w.EnsureInitialAdmin(ctx, member.InitialAdmin{Name: "管理员", Email: "admin@example.com", Password: "initial-password-123"}); err != nil {
		t.Fatal(err)
	}
	u, err := sqlc.New(pool).GetUserByEmail(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return w, u.ID
}

func TestMemberLifecycle(t *testing.T) {
	w, admin := fixture(t)
	ctx := t.Context()
	var group string
	if err := w.pool.QueryRow(ctx, "INSERT INTO groups(name) VALUES('测试分组') RETURNING id").Scan(&group); err != nil {
		t.Fatal(err)
	}
	user, err := w.CreateUser(ctx, member.CreateUser{ActorID: admin, Name: "成员", Email: "member@example.com", Role: "user", Password: "member-password", GroupIDs: []string{group}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.CreateUser(ctx, member.CreateUser{ActorID: admin, Name: "不存在的分组", Email: "rollback@example.com", Role: "user", GroupIDs: []string{admin}})
	if !errors.Is(err, member.ErrGroupUnavailable) {
		t.Fatalf("分组校验: %v", err)
	}
	var count int
	if err := w.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email='rollback@example.com'").Scan(&count); err != nil || count != 0 {
		t.Fatal("创建失败未回滚", err)
	}
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	changed, err := w.UpdateUser(ctx, tx, member.UpdateUser{ID: user.ID, Name: "已修改", Role: "user", Status: "disabled"})
	if err != nil || changed.Status != "disabled" {
		t.Fatal(changed, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	saved, err := readUser(ctx, sqlc.New(w.pool), user.ID)
	if err != nil || saved.Status != "active" {
		t.Fatal("调用方事务回滚失败", err)
	}
	tx, err = w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = (Groups{}).MoveMembers(ctx, tx, member.MoveMembers{ActorID: admin, Members: []member.MoveMember{{ID: user.ID, SourceGroupID: &group}}}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = w.pool.QueryRow(ctx, "SELECT count(*) FROM group_users WHERE user_id=$1 AND removed_at IS NULL", user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("移回根分组失败", err)
	}
	// 重复初始化不得覆盖管理员密码或增加账号。
	if err = w.EnsureInitialAdmin(ctx, member.InitialAdmin{}); err != nil {
		t.Fatal(err)
	}
}

func TestOIDCRegistration(t *testing.T) {
	w, admin := fixture(t)
	ctx := t.Context()
	in := member.OAuthIdentity{Provider: "oidc", Issuer: "https://id.example", Subject: "new-user", Name: "OIDC成员", Email: "new@example.com", EmailVerified: true, AutoRegistrationEnabled: true}
	first, err := w.UpsertIdentity(ctx, in)
	if err != nil || first.Role != "user" {
		t.Fatal(first, err)
	}
	in.AutoRegistrationEnabled = false
	again, err := w.UpsertIdentity(ctx, in)
	if err != nil || again.ID != first.ID {
		t.Fatal("重复登录失败", err)
	}
	in.Subject = "blocked"
	in.Email = "blocked@example.com"
	if _, err = w.UpsertIdentity(ctx, in); !errors.Is(err, member.ErrRegistrationClosed) {
		t.Fatal("注册开关失效", err)
	}
	in.AdminOnly = true
	in.AutoRegistrationEnabled = true
	if _, err = w.UpsertIdentity(ctx, in); !errors.Is(err, member.ErrAdminRoleRequired) {
		t.Fatal("允许自动注册管理员", err)
	}
	in.AdminOnly = false
	in.Subject = "unverified"
	in.Email = "admin@example.com"
	in.EmailVerified = false
	other, err := w.UpsertIdentity(ctx, in)
	if err != nil || other.ID == admin || other.Role != "user" || !strings.HasSuffix(other.Email, "@oidc.oauth.local") {
		t.Fatal("未验证邮箱关联了管理员", other, err)
	}
	in.Subject = "verified-admin"
	in.EmailVerified = true
	in.AdminOnly = true
	in.AutoRegistrationEnabled = false
	linked, err := w.UpsertIdentity(ctx, in)
	if err != nil || linked.ID != admin {
		t.Fatal("已验证邮箱未关联管理员", err)
	}
	if _, err = w.pool.Exec(ctx, "UPDATE users SET status='disabled',disabled_at=now() WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	in.Subject = "new-user"
	in.Email = "changed@example.com"
	in.AdminOnly = false
	if _, err = w.UpsertIdentity(ctx, in); !errors.Is(err, member.ErrUserDisabled) {
		t.Fatal("停用账号登录成功", err)
	}
}

func TestConcurrentOIDC(t *testing.T) {
	w, _ := fixture(t)
	in := member.OAuthIdentity{Provider: "oidc", Issuer: "https://id.example", Subject: "concurrent", AutoRegistrationEnabled: true}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := w.UpsertIdentity(t.Context(), in); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var count int
	if err := w.pool.QueryRow(t.Context(), "SELECT count(*) FROM user_identities WHERE provider_subject='concurrent'").Scan(&count); err != nil || count != 1 {
		t.Fatal("并发登录产生重复身份", count, err)
	}
}
