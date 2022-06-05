package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"

	"github.com/penguin-statistics/backend-next/internal/constant"
)

func TestV2Result(t *testing.T) {
	var app *fiber.App
	populate(t, &app)

	t.Run("GetsPrivateAPIs", func(t *testing.T) {
		sourcedPaths := []string{
			"/PenguinStats/api/v2/_private/result/matrix/:server/:source",
			"/PenguinStats/api/v2/_private/result/pattern/:server/:source",
		}

		testSource := func(path, source string, requestChanger func(*http.Request) *http.Request) {
			for _, server := range constant.Servers {
				t.Run("Gets"+strings.Title(source)+"ResultFor"+strings.Title(server), func(t *testing.T) {
					t.Parallel()
					replacedPath := strings.Replace(path, ":server", server, 1)
					replacedPath = strings.Replace(replacedPath, ":source", source, 1)

					req := httptest.NewRequest("GET", replacedPath, nil)
					req = requestChanger(req)

					resp, err := app.Test(req, 6000)
					if err != nil {
						t.Error(err)
					}

					assert.Equal(t, 200, resp.StatusCode, "expect success response")
				})
			}
		}

		for _, path := range sourcedPaths {
			// Test all source=global paths
			testSource(path, "global", func(req *http.Request) *http.Request {
				return req
			})

			// Test all source=personal paths
			testSource(path, "personal", func(req *http.Request) *http.Request {
				req.Header.Set("Authorization", "PenguinID test6")
				return req
			})
		}

		// Test trends
		testSource("/PenguinStats/api/v2/_private/result/trend/:server", "global", func(req *http.Request) *http.Request {
			return req
		})
	})

	t.Run("GetsPublicAPIs", func(t *testing.T) {
		sourcedPaths := []string{
			"/PenguinStats/api/v2/result/matrix?server=:server&is_personal=:isPersonal",
			"/PenguinStats/api/v2/result/pattern?server=:server&is_personal=:isPersonal",
		}

		testServer := func(path string, isPersonal bool, requestChanger func(*http.Request) *http.Request) {
			for _, server := range constant.Servers {
				replacedPath := strings.Replace(path, ":server", server, 1)
				replacedPath = strings.Replace(replacedPath, ":isPersonal", strconv.FormatBool(isPersonal), 1)

				req := httptest.NewRequest("GET", replacedPath, nil)
				req = requestChanger(req)

				resp, err := app.Test(req, 5000)
				if err != nil {
					t.Error(err)
				}

				assert.Equal(t, 200, resp.StatusCode, "expect success response")
			}
		}

		for _, path := range sourcedPaths {
			// Test all source=global paths
			testServer(path, false, func(req *http.Request) *http.Request {
				return req
			})

			// Test all source=personal paths
			testServer(path, true, func(req *http.Request) *http.Request {
				req.Header.Set("Authorization", "PenguinID test6")
				return req
			})
		}

		// Test trends
		testServer("/PenguinStats/api/v2/result/trends?server=:server&is_personal=:isPersonal", false, func(req *http.Request) *http.Request {
			return req
		})
	})
}
