package service

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/ahmetb/go-linq/v3"
	"github.com/antonmedv/expr"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/gamedata"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util"
	"exusiai.dev/backend-next/internal/util/reportverifs"
)

type Admin struct {
	DB                *bun.DB
	AdminRepo         *repo.Admin
	DropReportService *DropReport
	RejectRuleRepo    *repo.RejectRule
	ZoneService       *Zone
	StageService      *Stage
	ActivityService   *Activity
	TimeRangeService  *TimeRange
	DropInfoService   *DropInfo
}

func NewAdmin(
	db *bun.DB,
	adminRepo *repo.Admin,
	dropReportService *DropReport,
	rejectRuleRepo *repo.RejectRule,
	zoneService *Zone,
	stageService *Stage,
	activityService *Activity,
	timeRangeService *TimeRange,
	dropInfoService *DropInfo,
) *Admin {
	return &Admin{
		DB:                db,
		AdminRepo:         adminRepo,
		DropReportService: dropReportService,
		RejectRuleRepo:    rejectRuleRepo,
		ZoneService:       zoneService,
		StageService:      stageService,
		ActivityService:   activityService,
		TimeRangeService:  timeRangeService,
		DropInfoService:   dropInfoService,
	}
}

func (s *Admin) SaveRenderedObjects(ctx context.Context, objects *gamedata.RenderedObjects) error {
	var innerErr error
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var zoneId int
		var zones []*model.Zone
		if objects.Zone != nil {
			zones = []*model.Zone{objects.Zone}
			if err := s.AdminRepo.SaveZones(ctx, tx, &zones); err != nil {
				innerErr = err
				return err
			}
			zoneId = zones[0].ZoneID
		}

		if objects.Activity != nil {
			activities := []*model.Activity{objects.Activity}
			if err := s.AdminRepo.SaveActivities(ctx, tx, &activities); err != nil {
				innerErr = err
				return err
			}
		}

		var rangeId int
		var timeRanges []*model.TimeRange
		if objects.TimeRange != nil {
			timeRanges = []*model.TimeRange{objects.TimeRange}
			if err := s.AdminRepo.SaveTimeRanges(ctx, tx, &timeRanges); err != nil {
				innerErr = err
				return err
			}
			rangeId = timeRanges[0].RangeID
		}

		stageIdMap := make(map[string]int)
		if len(objects.Stages) > 0 {
			linq.From(objects.Stages).ForEachT(func(stage *model.Stage) {
				stage.ZoneID = zoneId
			})
			if err := s.AdminRepo.SaveStages(ctx, tx, &objects.Stages); err != nil {
				innerErr = err
				return err
			}
			linq.From(objects.Stages).
				ToMapByT(&stageIdMap,
					func(stage *model.Stage) string { return stage.ArkStageID },
					func(stage *model.Stage) int { return stage.StageID },
				)
		}

		if len(objects.DropInfosMap) > 0 {
			dropInfosToSave := make([]*model.DropInfo, 0)
			for arkStageId, dropInfos := range objects.DropInfosMap {
				stageId := stageIdMap[arkStageId]
				for _, dropInfo := range dropInfos {
					dropInfo.StageID = stageId
					dropInfo.RangeID = rangeId
					dropInfosToSave = append(dropInfosToSave, dropInfo)
				}
			}
			if err := s.AdminRepo.SaveDropInfos(ctx, tx, &dropInfosToSave); err != nil {
				innerErr = err
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// if no error, purge cache
	if innerErr == nil {
		// zone
		if objects.Zone != nil {
			cache.Zones.Delete()
			cache.ShimZones.Delete()
		}

		// activity
		if objects.Activity != nil {
			cache.Activities.Delete()
			cache.ShimActivities.Delete()
		}

		// timerange
		if objects.TimeRange != nil {
			cache.TimeRanges.Delete(objects.TimeRange.Server)
			cache.TimeRangesMap.Delete(objects.TimeRange.Server)
			cache.MaxAccumulableTimeRanges.Delete(objects.TimeRange.Server)
			cache.AllMaxAccumulableTimeRanges.Delete(objects.TimeRange.Server)
			cache.LatestTimeRanges.Delete(objects.TimeRange.Server)
		}

		// stage
		if len(objects.Stages) > 0 {
			cache.Stages.Delete()
			cache.StagesMapByID.Delete()
			cache.StagesMapByArkID.Delete()
			for _, server := range constant.Servers {
				cache.ShimStages.Delete(server)
			}
		}
	}

	return innerErr
}

func (s *Admin) CloneFromCN(ctx context.Context, req types.CloneFromCNRequest) error {
	type ZoneName struct {
		ZH string `json:"zh"`
		EN string `json:"en"`
		JA string `json:"ja"`
		KO string `json:"ko"`
	}

	type ServerExistence struct {
		Exist     bool  `json:"exist"`
		OpenTime  int64 `json:"openTime"`
		CloseTime int64 `json:"closeTime,omitempty"`
	}

	type Existence struct {
		CN ServerExistence `json:"CN"`
		US ServerExistence `json:"US"`
		JP ServerExistence `json:"JP"`
		KR ServerExistence `json:"KR"`
	}

	zone, err := s.ZoneService.GetZoneByArkId(ctx, req.ArkZoneID)
	if err != nil {
		return err
	}

	// get new zone name json
	zoneName := ZoneName{}
	if err := json.Unmarshal(zone.Name, &zoneName); err != nil {
		return err
	}
	givenZoneName := ZoneName{}
	if err := json.Unmarshal(req.ForeignZoneName, &givenZoneName); err != nil {
		return err
	}
	givenZoneName.ZH = zoneName.ZH
	newZoneName, err := json.Marshal(givenZoneName)
	if err != nil {
		return err
	}
	zone.Name = newZoneName

	activityUS := model.Activity{
		ActivityID: 0,
		Name:       zone.Name,
	}
	activityJPKR := model.Activity{
		ActivityID: 0,
		Name:       zone.Name,
	}

	// convert start and end date to foreign existence
	existence := Existence{}
	if err := json.Unmarshal(zone.Existence, &existence); err != nil {
		return err
	}
	const pattern = "2006-01-02T15:04:05-0700"
	v := reflect.ValueOf(req.ForeignTimeRange)
	for i := 0; i < v.NumField(); i++ {
		server := v.Type().Field(i).Name
		timeStruct := v.Field(i).Interface().(types.ForeignTimeRangeString)
		start, err := time.Parse(pattern, timeStruct.Start)
		if err != nil {
			return err
		}
		var end time.Time
		if timeStruct.End != "" {
			end, err = time.Parse(pattern, timeStruct.End)
			if err != nil {
				return err
			}
		}
		startMilli := util.GetTimeStampInServer(&start, server)
		var endMilli int64
		if timeStruct.End != "" {
			endMilli = util.GetTimeStampInServer(&end, server)
		}
		ServerExistence := ServerExistence{
			Exist:     true,
			OpenTime:  startMilli,
			CloseTime: endMilli,
		}
		existenceField := reflect.ValueOf(&existence).Elem().FieldByName(server)
		existenceField.Set(reflect.ValueOf(ServerExistence))

		startTime := time.UnixMilli(startMilli)
		endTime := time.UnixMilli(constant.FakeEndTimeMilli)
		if endMilli != 0 {
			endTime = time.UnixMilli(endMilli)
		}
		if server == "US" {
			activityUS.StartTime = &startTime
			activityUS.EndTime = &endTime
		} else if server == "JP" || server == "KR" {
			activityJPKR.StartTime = &startTime
			activityJPKR.EndTime = &endTime
		}
	}
	newExistence, err := json.Marshal(existence)
	if err != nil {
		return err
	}
	zone.Existence = newExistence

	stages, err := s.StageService.GetStagesByZoneId(ctx, zone.ZoneID)
	if err != nil {
		return err
	}
	for _, stage := range stages {
		stage.Existence = newExistence
	}

	// handle existing and 2 new activities
	activitiesToSave := []*model.Activity{}
	if req.ActivityID != 0 {
		activity, err := s.ActivityService.GetActivityById(ctx, req.ActivityID)
		if err != nil {
			return err
		}
		activity.Name = zone.Name

		activityUSExistence := []byte(`{"CN": {"exist": false}, "JP": {"exist": false}, "KR": {"exist": false}, "US": {"exist": true}}`)
		activityUS.Existence = (json.RawMessage)(activityUSExistence)
		activityJPKRExistence := []byte(`{"CN": {"exist": false}, "JP": {"exist": true}, "KR": {"exist": true}, "US": {"exist": false}}`)
		activityJPKR.Existence = (json.RawMessage)(activityJPKRExistence)

		activitiesToSave = append(activitiesToSave, activity, &activityUS, &activityJPKR)
	}

	// handle new timeRange
	timeRange, err := s.TimeRangeService.GetTimeRangeById(ctx, req.RangeID)
	if err != nil {
		return err
	}
	timeRangesToSave := []*model.TimeRange{}
	for _, server := range constant.Servers {
		if server == "CN" {
			continue
		}
		var startTime time.Time
		var endTime time.Time
		var comment string
		if server == "US" {
			startTime = *activityUS.StartTime
			endTime = *activityUS.EndTime
			comment = "美服"
		} else if server == "JP" || server == "KR" {
			startTime = *activityJPKR.StartTime
			endTime = *activityJPKR.EndTime
			if server == "JP" {
				comment = "日服"
			} else if server == "KR" {
				comment = "韩服"
			}
		}
		comment += givenZoneName.ZH +
			" " +
			startTime.In(constant.LocMap[server]).Format("2006/01/02 15:04") +
			" - "
		if endTime.UnixMilli() == constant.FakeEndTimeMilli {
			comment += "?"
		} else {
			comment += endTime.In(constant.LocMap[server]).Format("2006/01/02 15:04")
		}
		newTimeRange := &model.TimeRange{
			RangeID:   0,
			Name:      timeRange.Name,
			StartTime: &startTime,
			EndTime:   &endTime,
			Server:    server,
			Comment:   null.StringFrom(comment),
		}
		timeRangesToSave = append(timeRangesToSave, newTimeRange)
	}

	err = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		s.AdminRepo.SaveZones(ctx, tx, &([]*model.Zone{zone}))
		s.AdminRepo.SaveStages(ctx, tx, &stages)
		s.AdminRepo.SaveActivities(ctx, tx, &activitiesToSave)
		s.AdminRepo.SaveTimeRanges(ctx, tx, &timeRangesToSave)
		return nil
	})
	if err != nil {
		return err
	}

	// handle new dropInfos
	for _, server := range constant.Servers {
		if server == "CN" {
			continue
		}
		newTimeRange, err := s.TimeRangeService.GetTimeRangeByServerAndName(ctx, server, timeRange.Name.String)
		if err != nil {
			return err
		}
		err = s.DropInfoService.CloneDropInfosFromCN(ctx, timeRange.RangeID, newTimeRange.RangeID, server)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Admin) GetRejectRulesReportContext(ctx context.Context, req types.RejectRulesReevaluationPreviewRequest) ([]RejectRulesReevaluationEvaluationContext, error) {
	log.Info().
		Str("evt.name", "admin.reject_rules.get_report_context").
		Interface("req", req).
		Msg("fetching reports from database")

	type dropReportJoinedResult struct {
		bun.BaseModel `bun:"drop_reports,alias:dr"`

		model.DropReport
		model.DropReportExtra `sql:"dre"`
		ArkStageID            string `sql:"st.ark_stage_id" json:"arkStageId"`
	}

	var dropReports []dropReportJoinedResult

	err := s.DB.NewSelect().
		Model(&dropReports).
		ColumnExpr("dr.*").
		ColumnExpr("dre.*").
		ColumnExpr("st.stage_id").
		ColumnExpr("st.ark_stage_id").
		Where("dr.created_at >= ?", req.ReevaluateRange.From).
		Where("dr.created_at <= ?", req.ReevaluateRange.To).
		Join("JOIN drop_report_extras as dre ON dr.report_id = dre.report_id").
		Join("JOIN stages as st ON dr.stage_id = st.stage_id").
		Scan(ctx)
	if err != nil {
		return nil, err
	}

	log.Info().
		Str("evt.name", "admin.reject_rules.get_report_context").
		Interface("req", req).
		Msgf("fetched %d reports from database", len(dropReports))

	for i, dropReport := range dropReports {
		dropReports[i].DropReport.ReportID = dropReport.DropReportExtra.ReportID
		// fix report id scan
	}

	evalContexts := make([]RejectRulesReevaluationEvaluationContext, len(dropReports))
	for i, dropReport := range dropReports {
		var createdAt int64
		if dropReport.CreatedAt != nil {
			createdAt = dropReport.CreatedAt.UnixMicro()
		}

		originalReport := dropReport.DropReport

		evalContexts[i] = RejectRulesReevaluationEvaluationContext{
			EvaluateContext: &reportverifs.ReportContext{
				Task: &types.ReportTask{
					CreatedAt: createdAt,
					FragmentReportCommon: types.FragmentReportCommon{
						Server:  dropReport.Server,
						Source:  dropReport.SourceName,
						Version: dropReport.Version,
					},
					AccountID: dropReport.AccountID,
					IP:        dropReport.IP,
				},
				Report: &types.ReportTaskSingleReport{
					FragmentStageID: types.FragmentStageID{
						StageID: dropReport.ArkStageID,
					},
					Times:    dropReport.Times,
					Metadata: dropReport.Metadata,
				},
			},
			OriginalReport: &originalReport,
		}

	}

	log.Info().
		Str("evt.name", "admin.reject_rules.get_report_context").
		Interface("req", req).
		Msgf("transformed reports to %d evaluation contexts", len(evalContexts))

	return evalContexts, nil
}

type RejectRulesReevaluationEvaluationContext struct {
	OriginalReport  *model.DropReport           `json:"originalReport"`
	EvaluateContext *reportverifs.ReportContext `json:"evaluateContext"`
}

type RejectRulesReevaluationEvaluationResult struct {
	RejectRulesReevaluationEvaluationContext

	EvaluationShouldRejectToReliability *int `json:"evaluationShouldRejectToReliability"`
}

type RejectRulesReevaluationEvaluationResultSet []*RejectRulesReevaluationEvaluationResult

type RejectRulesReevaluationEvaluationResultSetSummaryReliabilityChanges struct {
	From int `json:"from"`
	To   int `json:"to"`

	Count int `json:"count"`
}

type RejectRulesReevaluationEvaluationResultSetSummarySampledEvaluations struct {
	ShouldReject []*RejectRulesReevaluationEvaluationResult `json:"shouldReject"`
	ShouldPass   []*RejectRulesReevaluationEvaluationResult `json:"shouldPass"`
}

type RejectRulesReevaluationEvaluationResultSetSummary struct {
	TotalCount         int                                                                   `json:"totalCount"`
	ChangeSetCount     int                                                                   `json:"changeSetCount"`
	ReliabilityChanges []RejectRulesReevaluationEvaluationResultSetSummaryReliabilityChanges `json:"reliabilityChanges"`

	SampledEvaluations RejectRulesReevaluationEvaluationResultSetSummarySampledEvaluations `json:"sampledEvaluations"`
}

type RejectRulesReevaluationEvaluationResultSetDiff struct {
	ReportID        int `json:"reportId"`
	FromReliability int `json:"fromReliability"`
	ToReliability   int `json:"toReliability"`
}

type RejectRulesReevaluationEvaluationResultSetChangeSet []*RejectRulesReevaluationEvaluationResultSetDiff

func (s RejectRulesReevaluationEvaluationResultSet) ChangeSet() RejectRulesReevaluationEvaluationResultSetChangeSet {
	changeSet := make(RejectRulesReevaluationEvaluationResultSetChangeSet, 0, len(s))
	for _, result := range s {
		originalReliability := result.OriginalReport.Reliability
		toReliability := originalReliability
		if result.EvaluationShouldRejectToReliability != nil {
			toReliability = *result.EvaluationShouldRejectToReliability
		}

		if originalReliability == toReliability {
			continue
		}

		changeSet = append(changeSet, &RejectRulesReevaluationEvaluationResultSetDiff{
			ReportID:        result.OriginalReport.ReportID,
			FromReliability: originalReliability,
			ToReliability:   toReliability,
		})
	}

	return changeSet
}

func (s RejectRulesReevaluationEvaluationResultSet) Summary() RejectRulesReevaluationEvaluationResultSetSummary {
	log.Debug().
		Str("evt.name", "admin.reject_rules.reevaluation.evaluation_result_set.summary").
		Msg("calculating summary")

	summary := RejectRulesReevaluationEvaluationResultSetSummary{
		TotalCount:         len(s),
		ReliabilityChanges: make([]RejectRulesReevaluationEvaluationResultSetSummaryReliabilityChanges, 0),
		SampledEvaluations: RejectRulesReevaluationEvaluationResultSetSummarySampledEvaluations{
			ShouldReject: make([]*RejectRulesReevaluationEvaluationResult, 0, 10),
			ShouldPass:   make([]*RejectRulesReevaluationEvaluationResult, 0, 10),
		},
	}

	changeset := s.ChangeSet()

	// ReliabilityChanges count (into a temp map)
	reliabilityChanges := map[int]map[int]int{}
	for _, change := range changeset {
		if reliabilityChanges[change.FromReliability] == nil {
			reliabilityChanges[change.FromReliability] = map[int]int{}
		}

		reliabilityChanges[change.FromReliability][change.ToReliability]++
	}

	// ReliabilityChanges count (into summary format)
	for originalReliability, changes := range reliabilityChanges {
		for changeTo, count := range changes {
			summary.ReliabilityChanges = append(summary.ReliabilityChanges, RejectRulesReevaluationEvaluationResultSetSummaryReliabilityChanges{
				From: originalReliability,
				To:   changeTo,

				Count: count,
			})
		}
	}

	// ReliabilityChanges sort
	sort.Slice(summary.ReliabilityChanges, func(i, j int) bool {
		return summary.ReliabilityChanges[i].Count > summary.ReliabilityChanges[j].Count
	})

	// SampledEvaluations
	for _, result := range s {
		if len(summary.SampledEvaluations.ShouldReject) >= 10 && len(summary.SampledEvaluations.ShouldPass) >= 10 {
			break // early break
		}

		if result.EvaluationShouldRejectToReliability != nil {
			if len(summary.SampledEvaluations.ShouldReject) >= 10 {
				continue
			}
			summary.SampledEvaluations.ShouldReject = append(summary.SampledEvaluations.ShouldReject, result)
		} else {
			if len(summary.SampledEvaluations.ShouldPass) >= 10 {
				continue
			}
			summary.SampledEvaluations.ShouldPass = append(summary.SampledEvaluations.ShouldPass, result)
		}
	}

	summary.ChangeSetCount = len(changeset)

	return summary
}

func (s *Admin) EvaluateRejectRules(ctx context.Context, evaluationContexts []RejectRulesReevaluationEvaluationContext, ruleId int) (RejectRulesReevaluationEvaluationResultSet, error) {
	log.Info().
		Str("evt.name", "admin.reject_rules.evaluate_reject_rules").
		Int("evaluation_contexts_count", len(evaluationContexts)).
		Int("rule_id", ruleId).
		Msg("fetching reject rule")

	rule, err := s.RejectRuleRepo.GetRejectRule(ctx, ruleId)
	if err != nil {
		return nil, err
	}

	if rule.Status != 1 {
		return nil, errors.New("rule is not active")
	}

	var evalResult any
	var evalErr error

	log.Info().
		Str("evt.name", "admin.reject_rules.evaluate_reject_rules").
		Int("evaluation_contexts_count", len(evaluationContexts)).
		Int("rule_id", ruleId).
		Msg("evaluating reject rule")

	evaluationResults := make([]*RejectRulesReevaluationEvaluationResult, len(evaluationContexts))
	for i, evaluationContext := range evaluationContexts {
		evalResult, evalErr = expr.Eval(rule.Expr, *evaluationContext.EvaluateContext)
		if evalErr != nil {
			return nil, errors.New("failed to evaluate rule: " + evalErr.Error())
		}

		shouldReject, ok := evalResult.(bool)
		if !ok {
			return nil, errors.New("evaluation result is not a boolean")
		}

		var evaluationShouldRejectToReliability *int
		if shouldReject {
			evaluationShouldRejectToReliability = &rule.WithReliability
		}

		evaluationResults[i] = &RejectRulesReevaluationEvaluationResult{
			RejectRulesReevaluationEvaluationContext: evaluationContext,
			EvaluationShouldRejectToReliability:      evaluationShouldRejectToReliability,
		}

		if i%50000 == 0 {
			log.Info().
				Str("evt.name", "admin.reject_rules.evaluate_reject_rules").
				Int("evaluation_contexts_count", len(evaluationContexts)).
				Int("evaluation_contexts_processed", i).
				Int("rule_id", ruleId).
				Msgf("evaluated context %d/%d", i, len(evaluationContexts))
		}
	}

	log.Info().
		Str("evt.name", "admin.reject_rules.evaluate_reject_rules").
		Int("evaluation_contexts_count", len(evaluationContexts)).
		Int("rule_id", ruleId).
		Msg("evaluated reject rule")

	return evaluationResults, nil
}
