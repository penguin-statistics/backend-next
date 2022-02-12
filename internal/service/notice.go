package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type NoticeService struct {
	NoticeRepo *repos.NoticeRepo
}

func NewNoticeService(noticeRepo *repos.NoticeRepo) *NoticeService {
	return &NoticeService{
		NoticeRepo: noticeRepo,
	}
}

// Cache: Notices, 24hrs
func (s *NoticeService) GetNotices(ctx *fiber.Ctx) ([]*models.Notice, error) {
	return s.NoticeRepo.GetNotices(ctx.Context())
}
