package test

// synthetics_const is for defining tests that requires constant values used
// from its internal packages.
// The reason to not directly use the constant values from the internal packages
// is to explicitly define the tests that depends on the constant values, so
// that if a constant value is changed unexpectedly, the tests will fail accordingly,
// and the developer will be aware of the change.
const (
	// ReportHashLen is the length of the report hash.
	// Used by POST /PenguinStats/api/v2/report
	ReportHashLen = len("cfmsmv1i32o8ob8jp7g0-1wE2I9dvMFXXzBMp")

	ReportValidBody = `{"server":"CN","source":"MeoAssistant","stageId":"wk_kc_5","drops":[{"dropType":"NORMAL_DROP","itemId":"2002","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2003","quantity":1},{"dropType":"NORMAL_DROP","itemId":"2004","quantity":3}],"version":"v3.0.4"}`
)
