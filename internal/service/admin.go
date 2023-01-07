package service

import (
	"context"
	"sort"

	"exusiai.dev/gommon/constant"
	"github.com/ahmetb/go-linq/v3"
	"github.com/antonmedv/expr"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/gamedata"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util/reportverifs"
)

type Admin struct {
	DB                *bun.DB
	AdminRepo         *repo.Admin
	DropReportService *DropReport
	RejectRuleRepo    *repo.RejectRule
}

func NewAdmin(db *bun.DB, adminRepo *repo.Admin, dropReportService *DropReport, rejectRuleRepo *repo.RejectRule) *Admin {
	return &Admin{
		DB:                db,
		AdminRepo:         adminRepo,
		DropReportService: dropReportService,
		RejectRuleRepo:    rejectRuleRepo,
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

	err := s.DropReportService.DropReportRepo.DB.NewSelect().
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
			createdAt = dropReport.CreatedAt.Unix()
		}

		originalReport := dropReport.DropReport

		evalContexts[i] = RejectRulesReevaluationEvaluationContext{
			EvaluateContext: &reportverifs.ReportContext{
				Task: &types.ReportTask{
					CreatedAt: createdAt,
					FragmentReportCommon: types.FragmentReportCommon{
						Server:  dropReport.Server,
						Source:  dropReport.Source,
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
	ReliabilityChanges []RejectRulesReevaluationEvaluationResultSetSummaryReliabilityChanges `json:"reliabilityChanges"`

	SampledEvaluations RejectRulesReevaluationEvaluationResultSetSummarySampledEvaluations `json:"sampledEvaluations"`
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

	// ReliabilityChanges count (into a temp map)
	reliabilityChanges := map[int]map[int]int{}
	for _, result := range s {
		originalReliability := result.OriginalReport.Reliability
		toReliability := originalReliability
		if result.EvaluationShouldRejectToReliability != nil {
			toReliability = *result.EvaluationShouldRejectToReliability
		}

		if _, ok := reliabilityChanges[originalReliability]; !ok {
			reliabilityChanges[originalReliability] = map[int]int{}
		}

		reliabilityChanges[originalReliability][toReliability]++
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
