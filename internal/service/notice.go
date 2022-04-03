package service

import (
	"context"
	"time"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type NoticeService struct {
	NoticeRepo *repo.NoticeRepo
}

func NewNoticeService(noticeRepo *repo.NoticeRepo) *NoticeService {
	return &NoticeService{
		NoticeRepo: noticeRepo,
	}
}

// Cache: (singular) notices, 24hrs; records last modified time
func (s *NoticeService) GetNotices(ctx context.Context) ([]*models.Notice, error) {
	var noticesFromCache []*models.Notice
	err := cache.Notices.Get(&noticesFromCache)
	if err == nil {
		return noticesFromCache, nil
	}

	notices, err := s.NoticeRepo.GetNotices(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Notices.Set(notices, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[notices]", time.Now(), 0)
	}
	return notices, err
}
