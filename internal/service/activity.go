package service

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/gommon/constant"
)

type Activity struct {
	ActivityRepo *repo.Activity
}

func NewActivity(activityRepo *repo.Activity) *Activity {
	return &Activity{
		ActivityRepo: activityRepo,
	}
}

// Cache: (singular) activities, 1 hr; records last modified time
func (s *Activity) GetActivities(ctx context.Context) ([]*model.Activity, error) {
	var activities []*model.Activity
	err := cache.Activities.Get(&activities)
	if err == nil {
		return activities, nil
	}

	activities, err = s.ActivityRepo.GetActivities(ctx)
	if err != nil {
		return nil, err
	}
	cache.Activities.Set(activities, time.Minute*5)
	cache.LastModifiedTime.Set("[activities]", time.Now(), 0)
	return activities, err
}

// Cache: (singular) shimActivities, 1 hr; records last modified time
func (s *Activity) GetShimActivities(ctx context.Context) ([]*modelv2.Activity, error) {
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
	cache.ShimActivities.Set(shimActivities, time.Minute*5)
	cache.LastModifiedTime.Set("[shimActivities]", time.Now(), 0)
	return shimActivities, nil
}

func (s *Activity) applyShim(activity *model.Activity) *modelv2.Activity {
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
