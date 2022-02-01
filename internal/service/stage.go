package service

import "github.com/penguin-statistics/backend-next/internal/repos"

type StageService struct {
	StageRepo *repos.StageRepo
}

func NewStageService(stageRepo *repos.StageRepo) *StageService {
	return &StageService{
		StageRepo: stageRepo,
	}
}
