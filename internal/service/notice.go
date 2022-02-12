package service

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
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

// Cache: notices, 24hrs
func (s *NoticeService) GetNotices(ctx *fiber.Ctx) ([]*models.Notice, error) {
	var notices []*models.Notice
	err := cache.Notices.Get("", notices)
	if err == nil {
		return notices, nil
	}

	notices, err = s.NoticeRepo.GetNotices(ctx.Context())
	go cache.Notices.Set("", notices, 24*time.Hour)
	return notices, err
}
