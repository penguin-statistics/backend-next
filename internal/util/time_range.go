package util

import (
	"exusiai.dev/backend-next/internal/model"
)

func GetIntersection(timeRange1 *model.TimeRange, timeRange2 *model.TimeRange) *model.TimeRange {
	if timeRange1 == nil || timeRange2 == nil {
		return nil
	}

	if timeRange1.StartTime.After(*timeRange2.EndTime) || timeRange2.StartTime.After(*timeRange1.EndTime) {
		return nil
	}

	if timeRange1.StartTime.Before(*timeRange2.StartTime) {
		if timeRange1.EndTime.Before(*timeRange2.EndTime) {
			return &model.TimeRange{
				StartTime: timeRange2.StartTime,
				EndTime:   timeRange1.EndTime,
			}
		} else {
			return &model.TimeRange{
				StartTime: timeRange2.StartTime,
				EndTime:   timeRange2.EndTime,
			}
		}
	} else {
		if timeRange1.EndTime.Before(*timeRange2.EndTime) {
			return &model.TimeRange{
				StartTime: timeRange1.StartTime,
				EndTime:   timeRange1.EndTime,
			}
		} else {
			return &model.TimeRange{
				StartTime: timeRange1.StartTime,
				EndTime:   timeRange2.EndTime,
			}
		}
	}
}
