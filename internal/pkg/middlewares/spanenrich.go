package middlewares

import (
	"strconv"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/pkg/pgid"
	"exusiai.dev/backend-next/internal/util"
)

// The attributes EnrichServerSpan adds to the server span, named as the request log names the same fields where
// Datadog has no standard attribute of spans for them.
const (
	// the client's address (Datadog's standard attribute of spans: network.client.ip)
	attributeClientIP = attribute.Key("http.client_ip")
	// the PenguinID the request was made with, or the one created for it (usr.id: Datadog's user of the span)
	attributeUserID = attribute.Key("usr.id")
	// the internal id of the account, on the routes that resolve it (the report routes)
	attributeUserAccountID = attribute.Key("usr.account_id")
	// the idempotency of a request that sent an Idempotency-Key: hit (answered with the response saved for the key)
	// or saved (served, and its response saved)
	attributeIdempotency = attribute.Key("http.idempotency")
	// a 5xx that is an expected answer of its route (ExpectStatus), such as the 503 of a page asked for while the data
	// is calculated after a start: not an error of the backend, so its span is not one either
	attributeExpectedStatus = attribute.Key("http.expected_status")
)

// EnrichServerSpan adds to the request's server span, if it is sampled, who made the request (address, PenguinID,
// account) and what came of it (idempotency), as the request log has them: traces can then be searched by user or
// client, and a trace of a request shows whose it was without its logs. It also settles the status of a 5xx that
// carries no error: one its route expects (ExpectStatus) is no error of the span, and any other one gets an exception
// event naming the status, Datadog's error.message of the span, which a 5xx answered without a Go error would
// otherwise lack.
//
// It must come right after the tracing middleware (otelfiber), which starts the span and sets the status of the
// response once the handlers have returned: a status set to Ok here is final, and keeps otelfiber from making a 5xx
// an error.
func EnrichServerSpan() fiber.Handler {
	return func(c *fiber.Ctx) error {
		span := trace.SpanFromContext(c.UserContext())
		if !span.IsRecording() {
			return c.Next()
		}
		err := c.Next()

		attrs := make([]attribute.KeyValue, 0, 5)
		attrs = append(attrs, attributeClientIP.String(util.ExtractIP(c)))
		penguinID := pgid.Extract(c)
		if penguinID == "" {
			// created for this request (report routes): set in the response
			penguinID = c.GetRespHeader(constant.PenguinIDSetHeader)
		}
		if penguinID != "" {
			attrs = append(attrs, attributeUserID.String(penguinID))
		}
		if accountID, ok := c.Locals(constant.LocalsAccountIDKey).(int); ok {
			attrs = append(attrs, attributeUserAccountID.String(strconv.Itoa(accountID)))
		}
		if v := c.GetRespHeader(constant.IdempotencyHeader); v != "" {
			attrs = append(attrs, attributeIdempotency.String(v))
		}
		// with an error, the status is not known yet: the error handler sets it after (otelfiber records the error)
		if status := c.Response().StatusCode(); err == nil && status >= fiber.StatusInternalServerError {
			if expected, _ := c.Locals(localsKeyExpectedStatus).(bool); expected {
				attrs = append(attrs, attributeExpectedStatus.Bool(true))
				span.SetStatus(codes.Ok, "")
			} else {
				span.RecordError(httpStatusError{status: status})
			}
		}
		span.SetAttributes(attrs...)
		return err
	}
}

// httpStatusError is the error of a 5xx answered without a Go error: its own type (error.type of the span) keeps
// Datadog's Error Tracking from grouping these with every other error made by errors.New.
type httpStatusError struct {
	status int
}

func (e httpStatusError) Error() string {
	return strconv.Itoa(e.status) + " " + utils.StatusMessage(e.status)
}
