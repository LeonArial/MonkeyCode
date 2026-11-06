package localmember

import (
	"context"
	"errors"
	groupdb "github.com/chaitin/MonkeyCode/monkeyai/backend/internal/group/sqlc"
	"github.com/chaitin/MonkeyCode/monkeyai/backend/member"
	"github.com/jackc/pgx/v5"
)

// Groups 在调用方事务内维护分组关系，校验、审计由分组服务负责。
type Groups struct{}

func (Groups) SetMembers(ctx context.Context, tx pgx.Tx, in member.GroupMembers) error {
	q := groupdb.New(tx)
	if _, err := q.RemoveMembers(ctx, groupdb.RemoveMembersParams{GroupID: in.GroupID, UserIds: in.UserIDs}); err != nil {
		return err
	}
	_, err := q.AddMembers(ctx, groupdb.AddMembersParams{GroupID: in.GroupID, UserIds: in.UserIDs, AssignedByUserID: in.ActorID})
	return err
}
func (Groups) MoveMembers(ctx context.Context, tx pgx.Tx, in member.MoveMembers) error {
	q := groupdb.New(tx)
	for _, item := range in.Members {
		if item.SourceGroupID != nil && *item.SourceGroupID == member.RootGroupID {
			present, err := q.HasUserInActiveGroup(ctx, item.ID)
			if err != nil {
				return err
			}
			if present {
				return errors.New("成员归属已变化")
			}
		} else if item.SourceGroupID != nil {
			result, err := q.RemoveMember(ctx, groupdb.RemoveMemberParams{UserID: item.ID, GroupID: *item.SourceGroupID})
			if err != nil {
				return err
			}
			if result.RowsAffected() != 1 {
				return errors.New("成员归属已变化")
			}
		}
		if in.TargetGroupID != nil && *in.TargetGroupID != member.RootGroupID {
			if _, err := q.AddMember(ctx, groupdb.AddMemberParams{GroupID: *in.TargetGroupID, UserID: item.ID, AssignedByUserID: in.ActorID}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (Groups) RemoveAllMembers(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := groupdb.New(tx).RemoveAllMembers(ctx, id)
	return err
}
