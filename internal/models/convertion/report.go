package convertion

import "github.com/penguin-statistics/backend-next/internal/models/types"

func SingleReportRequestToSingleReport(request *types.SingleReportRequest) *types.SingleReport {
	return &types.SingleReport{
		FragmentStageID: request.FragmentStageID,
		Drops:           request.Drops,
	}
}
