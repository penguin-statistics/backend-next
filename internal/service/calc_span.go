package service

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// The attributes of the span of a matrix job (calcwkr.dropMatrix, calcwkr.patternMatrix): what it calculated, which
// tells a slow run from a large one, and the first run of a day, which also recalculates the day before, from the
// others.
const (
	// the day the job calculated (util.GetDayNum: days since the epoch, in the server's time zone)
	attributeCalcDayNum = attribute.Key("calc.day_num")
	// the source categories the job calculated, comma-separated (MatrixWorkerSourceCategories)
	attributeCalcSourceCategories = attribute.Key("calc.source_categories")
	// the matrix elements saved for the day
	attributeCalcElements = attribute.Key("calc.elements")
	// whether the job recalculated the day before too, as the first run of a day does, and the elements it saved
	attributeCalcPreviousDay         = attribute.Key("calc.previous_day")
	attributeCalcPreviousDayElements = attribute.Key("calc.previous_day_elements")
)

// describeCalcJob adds what a matrix job calculated to the span of ctx, if it is sampled.
func describeCalcJob(ctx context.Context, dayNum int, sourceCategories []string, elements int) {
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(
			attributeCalcDayNum.Int(dayNum),
			attributeCalcSourceCategories.String(strings.Join(sourceCategories, ",")),
			attributeCalcElements.Int(elements),
			attributeCalcPreviousDay.Bool(false),
		)
	}
}

// describePreviousDay adds to the span of ctx, if it is sampled, that the job recalculated the day before, and the
// elements it saved for it.
func describePreviousDay(ctx context.Context, elements int) {
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(attributeCalcPreviousDay.Bool(true), attributeCalcPreviousDayElements.Int(elements))
	}
}
