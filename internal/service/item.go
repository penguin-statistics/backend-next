package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
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

func (s *ItemService) GetItems(ctx *fiber.Ctx) ([]*models.Item, error) {
	return s.ItemRepo.GetItems(ctx.Context())
}

func (s *ItemService) GetItemById(ctx *fiber.Ctx, itemId int) (*models.Item, error) {
	return s.ItemRepo.GetItemById(ctx.Context(), itemId)
}

func (s *ItemService) GetItemByArkId(ctx *fiber.Ctx, arkItemId string) (*models.Item, error) {
	return s.ItemRepo.GetItemByArkId(ctx.Context(), arkItemId)
}

func (s *ItemService) GetShimItems(ctx *fiber.Ctx) ([]*shims.Item, error) {
	return s.ItemRepo.GetShimItems(ctx.Context())
}

func (s *ItemService) GetShimItemByArkId(ctx *fiber.Ctx, itemId string) (*shims.Item, error) {
	return s.ItemRepo.GetShimItemByArkId(ctx.Context(), itemId)
}
