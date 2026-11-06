// Package localmember 提供本机开发的成员及自定义 OIDC 身份写入。
package localmember

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/chaitin/MonkeyCode/monkeyai/backend/internal/identity/sqlc"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/member"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Writer 实现本机成员管理；邮箱验证码注册仍未启用。
type Writer struct {
	member.EmptyUserWriter
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Writer { return &Writer{pool: pool} }

func (w *Writer) EnsureInitialAdmin(ctx context.Context, in member.InitialAdmin) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	q := sqlc.New(tx)
	if _, err := q.LockInitialAdmin(ctx); err != nil {
		return err
	}
	count, err := q.CountUsers(ctx)
	if err != nil {
		return err
	}
	// 已有用户时不重置账号，保留用户修改过的密码。
	if count != 0 {
		return tx.Commit(ctx)
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(in.Password) < 12 {
		return fmt.Errorf("本地首次启动需要有效的管理员邮箱和至少 12 位密码")
	}
	hash, err := member.HashPassword(in.Password)
	if err != nil {
		return err
	}
	_, err = q.CreateInitialAdmin(ctx, sqlc.CreateInitialAdminParams{Name: in.Name, Email: email, PasswordHash: &hash})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *Writer) TouchLogin(ctx context.Context, id string) error {
	_, err := sqlc.New(w.pool).TouchLogin(ctx, id)
	return err
}
