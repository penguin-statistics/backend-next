package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"exusiai.dev/backend-next/internal/pkg/testentry"
	"exusiai.dev/backend-next/internal/service"
)

func TestAccounts(t *testing.T) {
	var s *service.Account
	testentry.Populate(t, &s)

	t.Run("ChecksAccountExists", func(t *testing.T) {
		ctx := context.Background()
		tests := []struct {
			accountId int
			exists    bool
		}{
			{1, true},
			{2, true},
			{3, true},
			{-1, false},
			{0, false},
		}

		for _, test := range tests {
			exists := s.IsAccountExistWithId(ctx, test.accountId)
			assert.Equal(t, test.exists, exists, "expect account to match exist status for account id %d", test.accountId)
		}
	})
}
