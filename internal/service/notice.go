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

// Cache: (singular) notices, 24hrs; records last modified time
func (s *NoticeService) GetNotices(ctx *fiber.Ctx) ([]*models.Notice, error) {
	var notices []*models.Notice
	err := cache.Notices.Get(notices)
	if err == nil {
		return notices, nil
	}

	notices, err = s.NoticeRepo.GetNotices(ctx.Context())
	if err := cache.Notices.Set(notices, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[notices]", time.Now(), 0)
	}
	return notices, err
}
