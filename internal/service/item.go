package service

import (
	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type ItemService struct {
	ItemRepo *repos.ItemRepo
}

func NewItemService(itemRepo *repos.ItemRepo) *ItemService {
	return &ItemService{
		ItemRepo: itemRepo,
	}
}

func (s *ItemService) GetItemsMap(ctx *fiber.Ctx) (map[int]*models.Item, error) {
	items, err := s.ItemRepo.GetItems(ctx.Context())
	if err != nil {
		return nil, err
	}
	itemsMap := make(map[int]*models.Item)
	linq.From(items).
		ToMapByT(
			&itemsMap,
			func(item *models.Item) int { return item.ItemID },
			func(item *models.Item) *models.Item { return item })
	return itemsMap, nil
}

func (s *ItemService) GetItemById(ctx *fiber.Ctx, itemId int) (*models.Item, error) {
	return s.ItemRepo.GetItemById(ctx.Context(), itemId)
}
