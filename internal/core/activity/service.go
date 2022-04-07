package activity

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
)

type Service struct {
	ActivityRepo *Repo
}

func NewService(activityRepo *Repo) *Service {
	return &Service{
		ActivityRepo: activityRepo,
	}
}

// Cache: (singular) activities, 24hrs; records last modified time
func (s *Service) GetActivities(ctx context.Context) ([]*Model, error) {
	var activities []*Model
	err := CacheActivities.Get(&activities)
	if err == nil {
		return activities, nil
	}

	activities, err = s.ActivityRepo.GetActivities(ctx)
	if err != nil {
		return nil, err
	}
	if err = CacheActivities.Set(activities, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[activities]", time.Now(), 0)
	}
	return activities, err
}

// Cache: (singular) shimActivities, 24hrs; records last modified time
func (s *Service) GetShimActivities(ctx context.Context) ([]*modelv2.Activity, error) {
	var shimActivitiesFromCache []*modelv2.Activity
	err := cache.ShimActivities.Get(&shimActivitiesFromCache)
	if err == nil {
		return shimActivitiesFromCache, nil
	}

	activities, err := s.ActivityRepo.GetActivities(ctx)
	if err != nil {
		return nil, err
	}
	shimActivities := make([]*modelv2.Activity, len(activities))
	for i, activity := range activities {
		shimActivities[i] = s.applyShim(activity)
	}
	if err := cache.ShimActivities.Set(shimActivities, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[shimActivities]", time.Now(), 0)
	}
	return shimActivities, nil
}

func (s *Service) applyShim(activity *Model) *modelv2.Activity {
	shimActivity := &modelv2.Activity{
		Existence: activity.Existence,
		LabelI18n: activity.Name,
		Start:     activity.StartTime.UnixMilli(),
	}
	if activity.EndTime != nil && activity.EndTime.UnixMilli() != constant.FakeEndTimeMilli {
		endTime := null.NewInt(activity.EndTime.UnixMilli(), true)
		shimActivity.End = endTime
	}
	return shimActivity
}
