package main

import (
	"io/ioutil"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/penguin-statistics/backend-next/cmd/test/comparator"
	"github.com/stretchr/testify/assert"
)

func TestV2Zones(t *testing.T) {
	var app *fiber.App
	populate(&app)

	t.Run("GetsShimFormatZones", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest("GET", "/api/v2/zones", nil))
		if err != nil {
			t.Error(err)
		}

		assert.Equal(t, 200, resp.StatusCode, "expect success response")

		b, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err, "expect no error while reading body")

		comparator, err := comparator.NewComparatorFromFilePath("../../test/testdata/v2_zone.json")
		assert.NoError(t, err, "expect no error while creating comparator")

		comp := comparator.Compare(b, []string{"subType", "background"})
		assert.NoError(t, comp, "expect response structure to match test data")
	})
}
