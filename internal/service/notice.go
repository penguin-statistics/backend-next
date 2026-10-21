package service

import (
	"context"
	"time"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/repo"
)

type Notice struct {
	NoticeRepo *repo.Notice
}

func NewNotice(noticeRepo *repo.Notice) *Notice {
	return &Notice{
		NoticeRepo: noticeRepo,
	}
}

// Cache: (singular) notices, 10 seconds; records last modified time
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
	cache.SetLastModifiedTime("[notices]", notices, func() {
		cache.Notices.Set(notices, time.Second*10)
	})
	return notices, err
}
