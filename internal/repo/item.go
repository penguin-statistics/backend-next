package repo

import (
	"context"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type Item struct {
	db    *bun.DB
	v2sel selector.S[modelv2.Item]
	v3sel selector.S[model.Item]
}

func NewItem(db *bun.DB) *Item {
	return &Item{
		db:    db,
		v2sel: selector.New[modelv2.Item](db),
		v3sel: selector.New[model.Item](db),
	}
}

func (r *Item) GetItems(ctx context.Context) ([]*model.Item, error) {
	return r.v3sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("item_id ASC")
	})
}

func (r *Item) GetItemById(ctx context.Context, itemId int) (*model.Item, error) {
	return r.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("item_id = ?", itemId)
	})
}

func (r *Item) GetItemByArkId(ctx context.Context, arkItemId string) (*model.Item, error) {
	return r.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("ark_item_id = ?", arkItemId)
	})
}

func (r *Item) GetShimItems(ctx context.Context) ([]*modelv2.Item, error) {
	return r.v2sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("item_id ASC")
	})
}

func (r *Item) GetShimItemByArkId(ctx context.Context, itemId string) (*modelv2.Item, error) {
	return r.v2sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("ark_item_id = ?", itemId)
	})
}

func (r *Item) SearchItemByName(ctx context.Context, name string) (*model.Item, error) {
	return r.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("\"name\"::TEXT ILIKE ?", "%"+name+"%")
	})
}

func (r *Item) GetRecruitTagItems(ctx context.Context) ([]*model.Item, error) {
	return r.v3sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("type = ?", constant.RecruitItemType).Order("item_id ASC")
	})
}
