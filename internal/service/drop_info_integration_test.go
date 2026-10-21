package service

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/samber/lo"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

func TestOpeningStageIdsMatchLegacy(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedOpeningStages(t, db)
	// JP has a current time range without drop infos, KR no time range at all
	now := time.Now()
	mustExec(t, db, "INSERT INTO time_ranges (name, start_time, end_time, server) VALUES ('opening', ?, ?, 'JP')", now.Add(-time.Hour), now.Add(time.Hour))
	dropInfoRepo := repo.NewDropInfo(db)
	s := NewDropInfo(dropInfoRepo, NewTimeRange(repo.NewTimeRange(db), dropInfoRepo))

	for server, want := range map[string][]int{"CN": {2, 5}, "US": {1}, "JP": {}, "KR": {}} {
		got, err := s.GetOpeningStageIdsByServer(ctx, server)
		if err != nil {
			t.Fatal(err)
		}
		sort.Ints(got)
		currentDropInfos, err := legacyGetCurrentDropInfosByServer(ctx, s, server)
		if err != nil {
			t.Fatal(err)
		}
		legacy := lo.Uniq(lo.Map(currentDropInfos, func(dropInfo *model.DropInfo, _ int) int { return dropInfo.StageID }))
		sort.Ints(legacy)
		if fmt.Sprint(got) != fmt.Sprint(want) || fmt.Sprint(legacy) != fmt.Sprint(want) {
			t.Errorf("%s: opening stages %v, v3.14.18 %v, want %v", server, got, legacy, want)
		}
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.GetOpeningStageIdsByServer(cancelled, "CN"); err == nil {
		t.Error("reading the opening stages with a cancelled context succeeded")
	}
}
