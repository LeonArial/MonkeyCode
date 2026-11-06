package localmember

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/chaitin/MonkeyCode/monkeyai/backend/internal/identity/sqlc"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/member"
	"github.com/jackc/pgx/v5"
)

func readUser(ctx context.Context, q *sqlc.Queries, id string) (member.User, error) {
	row, err := q.GetUser(ctx, id)
	return member.User{ID: row.ID, Name: row.Name, Email: row.Email, AvatarURL: row.AvatarUrl, Role: row.Role, Status: row.Status, JoinedAt: row.JoinedAt, LastLoginAt: row.LastLoginAt}, err
}

// UpsertIdentity 只接入管理员配置的自定义 OIDC，始终以 issuer 与 subject 定位身份。
func (w *Writer) UpsertIdentity(ctx context.Context, in member.OAuthIdentity) (member.User, error) {
	if in.Provider != "oidc" {
		return member.User{}, member.ErrWriterUnavailable
	}
	if in.Issuer == "" || in.Subject == "" {
		return member.User{}, fmt.Errorf("OIDC 身份缺少 issuer 或 subject")
	}
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return member.User{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	q := sqlc.New(tx)
	// 与管理员新增成员及分组变更使用同一事务锁，防止并发注册重复绑定。
	if err := q.LockUserCreationGroups(ctx); err != nil {
		return member.User{}, err
	}
	identity, err := q.GetIdentityUser(ctx, sqlc.GetIdentityUserParams{Provider: in.Provider, Issuer: in.Issuer, ProviderSubject: in.Subject})
	bound := err == nil
	id := identity.ID
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return member.User{}, err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	// 未验证的邮箱不用于关联已有账号，避免接管同邮箱的管理员。
	if email == "" || !in.EmailVerified {
		digest := sha256.Sum256([]byte(in.Issuer + "\x00" + in.Subject))
		email = fmt.Sprintf("%x@oidc.oauth.local", digest)
	}
	if !bound {
		existing, lookupErr := q.GetUserByEmail(ctx, email)
		err = lookupErr
		id = existing.ID
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return member.User{}, err
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = in.Username
	}
	if name == "" {
		name = email
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if in.AdminOnly {
			return member.User{}, member.ErrAdminRoleRequired
		}
		if !in.AutoRegistrationEnabled {
			return member.User{}, member.ErrRegistrationClosed
		}
		row, createErr := q.CreateIdentityUser(ctx, sqlc.CreateIdentityUserParams{Name: name, Email: email, AvatarUrl: in.AvatarURL})
		if createErr != nil {
			return member.User{}, createErr
		}
		id = row.ID
	}
	user, err := readUser(ctx, q, id)
	if err != nil {
		return member.User{}, err
	}
	if user.Status != "active" {
		return member.User{}, member.ErrUserDisabled
	}
	if in.AdminOnly && user.Role != "admin" {
		return member.User{}, member.ErrAdminRoleRequired
	}
	if _, err := q.UpdateIdentityUser(ctx, sqlc.UpdateIdentityUserParams{ID: id, Name: name, AvatarUrl: in.AvatarURL}); err != nil {
		return member.User{}, err
	}
	if !bound {
		if _, err := q.UpsertIdentity(ctx, sqlc.UpsertIdentityParams{UserID: id, Provider: in.Provider, Issuer: in.Issuer, ProviderSubject: in.Subject, ProviderUsername: in.Username, ProviderEmail: in.Email, ProviderAvatarUrl: in.AvatarURL}); err != nil {
			return member.User{}, err
		}
	}
	user, err = readUser(ctx, q, id)
	if err != nil {
		return member.User{}, err
	}
	return user, tx.Commit(ctx)
}

func (w *Writer) ResetPassword(ctx context.Context, tx pgx.Tx, in member.PasswordReset) (string, error) {
	return sqlc.New(tx).ResetPassword(ctx, sqlc.ResetPasswordParams{Email: in.Email, PasswordHash: &in.PasswordHash})
}
