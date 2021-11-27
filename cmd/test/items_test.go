package main

import (
	"io/ioutil"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestV2Items(t *testing.T) {
	var app *fiber.App
	populate(&app)

	t.Run("GetsShimFormatItems", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest("GET", "/api/v2/items", nil))
		if err != nil {
			t.Error(err)
		}

		assert.Equal(t, 200, resp.StatusCode, "expect success response")

		b, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err, "expect no error while reading body")

		j := gjson.ParseBytes(b)

		j.ForEach(func(key, value gjson.Result) bool {
			assert.Equal(t, gjson.String, value.Get("itemId").Type, "expect itemId is a string")

			coords := value.Get("spriteCoords")
			if coords.IsArray() {
				arr := coords.Array()
				assert.Equal(t, len(arr), 2, "expect spriteCoords is an array of length 2")

				for _, v := range arr {
					assert.Equal(t, gjson.Number, v.Type, "expect spriteCoords is an array of numbers")
				}
			}

			return true
		})
	})
}
