package service

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type ActivityService struct {
	ActivityRepo *repos.ActivityRepo
}

func NewActivityService(activityRepo *repos.ActivityRepo) *ActivityService {
	return &ActivityService{
		ActivityRepo: activityRepo,
	}
}

// Cache: (singular) activities, 24hrs; records last modified time
func (s *ActivityService) GetActivities(ctx context.Context) ([]*models.Activity, error) {
	var activities []*models.Activity
	err := cache.Activities.Get(activities)
	if err == nil {
		return activities, nil
	}

	activities, err = s.ActivityRepo.GetActivities(ctx)
	if err := cache.Activities.Set(activities, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[activities]", time.Now(), 0)
	}
	return activities, err
}

// Cache: (singular) shimActivities, 24hrs; records last modified time
func (s *ActivityService) GetShimActivities(ctx context.Context) ([]*shims.Activity, error) {
	var shimActivitiesFromCache []*shims.Activity
	err := cache.ShimActivities.Get(&shimActivitiesFromCache)
	if err == nil {
		return shimActivitiesFromCache, nil
	}

	activities, err := s.ActivityRepo.GetActivities(ctx)
	if err != nil {
		return nil, err
	}
	shimActivities := make([]*shims.Activity, len(activities))
	for i, activity := range activities {
		shimActivities[i] = s.applyShim(activity)
	}
	if err := cache.ShimActivities.Set(shimActivities, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[shimActivities]", time.Now(), 0)
	}
	return shimActivities, nil
}

func (s *ActivityService) applyShim(activity *models.Activity) *shims.Activity {
	shimActivity := &shims.Activity{
		Existence: activity.Existence,
		LabelI18n: activity.Name,
		Start:     activity.StartTime.UnixMilli(),
	}
	if activity.EndTime != nil && activity.EndTime.UnixMilli() != constants.FakeEndTimeMilli {
		endTime := null.NewInt(activity.EndTime.UnixMilli(), true)
		shimActivity.End = &endTime
	}
	return shimActivity
}
