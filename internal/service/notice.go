package service

import (
	"context"
	"time"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type NoticeService struct {
	NoticeRepo *repo.Notice
}

func NewNoticeService(noticeRepo *repo.Notice) *NoticeService {
	return &NoticeService{
		NoticeRepo: noticeRepo,
	}
}

// Cache: (singular) notices, 24hrs; records last modified time
func (s *NoticeService) GetNotices(ctx context.Context) ([]*model.Notice, error) {
	var noticesFromCache []*model.Notice
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
