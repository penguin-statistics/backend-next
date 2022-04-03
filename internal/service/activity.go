package service

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type ActivityService struct {
	ActivityRepo *repo.Activity
}

func NewActivityService(activityRepo *repo.Activity) *ActivityService {
	return &ActivityService{
		ActivityRepo: activityRepo,
	}
}

// Cache: (singular) activities, 24hrs; records last modified time
func (s *ActivityService) GetActivities(ctx context.Context) ([]*model.Activity, error) {
	var activities []*model.Activity
	err := cache.Activities.Get(&activities)
	if err == nil {
		return activities, nil
	}

	activities, err = s.ActivityRepo.GetActivities(ctx)
	if err != nil {
		return nil, err
	}
	if err = cache.Activities.Set(activities, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[activities]", time.Now(), 0)
	}
	return activities, err
}

// Cache: (singular) shimActivities, 24hrs; records last modified time
func (s *ActivityService) GetShimActivities(ctx context.Context) ([]*modelv2.Activity, error) {
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

func (s *ActivityService) applyShim(activity *model.Activity) *modelv2.Activity {
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
