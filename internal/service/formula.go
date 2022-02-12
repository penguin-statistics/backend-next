package service

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
)

type FormulaService struct{}

func NewFormulaService() *FormulaService {
	return &FormulaService{}
}

// Cache: formula, 24hrs
func (s *FormulaService) GetFormula() (interface{}, error) {
	var formula interface{}
	err := cache.Formula.Get("formula", &formula)
	if err == nil {
		return formula, nil
	}

	// TODO: get formula from DB (properties) when it's ready
	res, err := http.Get(constants.FormulaFilePath)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get formula")
	}

	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(body), &formula)
	cache.Formula.Set("", formula, 24*time.Hour)

	return formula, nil
}
