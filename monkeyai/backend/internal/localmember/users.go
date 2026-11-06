package localmember

import (
	"context"
	"errors"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/internal/identity/sqlc"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/member"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"time"
)

func (w *Writer) CreateUser(ctx context.Context, in member.CreateUser) (member.User, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return member.User{}, err
	}
	defer func() {
		if err := tx.Rollback(context.WithoutCancel(ctx)); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Error("回滚事务失败", "module", "localmember", "operation", "CreateUser", "error", err)
		}
	}()
	q := sqlc.New(tx)
	if err = q.LockUserCreationGroups(ctx); err != nil {
		return member.User{}, err
	}
	if len(in.GroupIDs) > 0 {
		rows, err := q.GetUserCreationGroups(ctx, in.GroupIDs)
		if err != nil {
			return member.User{}, err
		}
		if len(rows) != len(in.GroupIDs) {
			return member.User{}, member.ErrGroupUnavailable
		}
	}
	var hash *string
	if in.Password != "" {
		value, err := member.HashPassword(in.Password)
		if err != nil {
			return member.User{}, err
		}
		hash = new(value)
	}
	row, err := q.CreateUser(ctx, sqlc.CreateUserParams{Name: in.Name, Email: in.Email, Role: in.Role, PasswordHash: hash})
	if err != nil {
		return member.User{}, err
	}
	if len(in.GroupIDs) > 0 {
		if err = q.TouchUserCreationGroups(ctx, in.GroupIDs); err != nil {
			return member.User{}, err
		}
		if err = q.AssignCreatedUserGroups(ctx, sqlc.AssignCreatedUserGroupsParams{GroupIds: in.GroupIDs, UserID: row.ID, ActorID: in.ActorID}); err != nil {
			return member.User{}, err
		}
	}
	return member.User{ID: row.ID, Name: row.Name, Email: row.Email, AvatarURL: row.AvatarUrl, Role: row.Role, Status: row.Status, JoinedAt: row.JoinedAt, LastLoginAt: row.LastLoginAt}, tx.Commit(ctx)
}
func (w *Writer) UpdateUser(ctx context.Context, tx pgx.Tx, in member.UpdateUser) (member.User, error) {
	var disabled *time.Time
	if in.Status == "disabled" {
		disabled = new(time.Now())
	}
	row, err := sqlc.New(tx).UpdateUser(ctx, sqlc.UpdateUserParams{ID: in.ID, Name: in.Name, Role: in.Role, Status: in.Status, PasswordHash: in.PasswordHash, DisabledAt: disabled})
	return member.User{ID: row.ID, Name: row.Name, Email: row.Email, AvatarURL: row.AvatarUrl, Role: row.Role, Status: row.Status, JoinedAt: row.JoinedAt, LastLoginAt: row.LastLoginAt}, err
}
func (w *Writer) ResetUserPassword(ctx context.Context, tx pgx.Tx, id, hash string) error {
	_, err := sqlc.New(tx).ResetUserPassword(ctx, sqlc.ResetUserPasswordParams{ID: id, PasswordHash: new(hash)})
	return err
}
