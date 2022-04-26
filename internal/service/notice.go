package service

import (
	"context"
	"time"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type Notice struct {
	NoticeRepo *repo.Notice
}

func NewNotice(noticeRepo *repo.Notice) *Notice {
	return &Notice{
		NoticeRepo: noticeRepo,
	}
}

// Cache: (singular) notices, 24hrs; records last modified time
func (s *Notice) GetNotices(ctx context.Context) ([]*model.Notice, error) {
	var noticesFromCache []*model.Notice
	err := cache.Notices.Get(&noticesFromCache)
	if err == nil {
		return noticesFromCache, nil
	}

	notices, err := s.NoticeRepo.GetNotices(ctx)
	if err != nil {
		return nil, err
	}
	cache.Notices.Set(notices, 24*time.Hour)
	cache.LastModifiedTime.Set("[notices]", time.Now(), 0)
	return notices, err
}
