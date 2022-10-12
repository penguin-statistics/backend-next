package main

import (
	"context"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"

	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/testentry"
	"github.com/penguin-statistics/backend-next/internal/service"
)

func TestPipelineMergeDropsAndMapDropTypes(t *testing.T) {
	var reportSvc *service.Report
	testentry.Populate(t, &reportSvc)

	type testCase struct {
		args   []types.ArkDrop
		expect []*types.Drop
	}

	testCases := []testCase{
		{
			args: []types.ArkDrop{
				{"REGULAR_DROP", "30013", 1},
				{"REGULAR_DROP", "30013", 1},
				{"REGULAR_DROP", "30013", 1},
			},
			expect: []*types.Drop{
				{"REGULAR", 8, 3},
			},
		},
		{
			args: []types.ArkDrop{
				{"REGULAR_DROP", "30013", 1},
				{"REGULAR_DROP", "30013", 1},
				{"REGULAR_DROP", "30012", 1},
			},
			expect: []*types.Drop{
				{"REGULAR", 8, 2},
				{"REGULAR", 7, 1},
			},
		},
		{
			args: []types.ArkDrop{
				{"NORMAL_DROP", "30013", 1},
				{"NORMAL_DROP", "30013", 1},
				{"NORMAL_DROP", "30012", 4},
			},
			expect: []*types.Drop{
				{"REGULAR", 8, 2},
				{"REGULAR", 7, 4},
			},
		},
		{
			args: []types.ArkDrop{
				{"SPECIAL_DROP", "30013", 1},
				{"SPECIAL_DROP", "30014", 1},
				{"SPECIAL_DROP", "30014", 1},
			},
			expect: []*types.Drop{
				{"SPECIAL", 8, 1},
				{"SPECIAL", 9, 2},
			},
		},
		{
			args: []types.ArkDrop{
				{"REGULAR_DROP", "30013", 1},
				{"NORMAL_DROP", "30013", 1},
			},
			expect: []*types.Drop{
				{"REGULAR", 8, 2},
			},
		},
		{
			args: []types.ArkDrop{
				{"REGULAR_DROP", "30013", 1},
				{"NORMAL_DROP", "30013", 1},
				{"SPECIAL_DROP", "30013", 1},
				{"SPECIAL_DROP", "30014", 1},
				{"SPECIAL_DROP", "30014", 1},
				{"NORMAL_DROP", "30013", 1},
				{"NORMAL_DROP", "30013", 1},
				{"NORMAL_DROP", "30012", 4},
			},
			expect: []*types.Drop{
				{"REGULAR", 8, 4},
				{"SPECIAL", 8, 1},
				{"SPECIAL", 9, 2},
				{"REGULAR", 7, 4},
			},
		},
	}

	for _, tc := range testCases {
		result, err := reportSvc.PipelineMergeDropsAndMapDropTypes(context.Background(), tc.args)
		assert.NoError(t, err, "expect no error")
		assert.ElementsMatchf(t, tc.expect, result, "expect result to match\nargs: %v\nexpect: %v\nresult: %v", tc.args, spew.Sdump(tc.expect), spew.Sdump(result))
	}
}
