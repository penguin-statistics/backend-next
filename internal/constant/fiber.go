package constant

const (
	ContextKeyRequestID = "requestid"

	IdempotencyHeader    = "X-Penguin-Idempotency"
	IdempotencyKeyHeader = "X-Penguin-Idempotency-Key"

	IdempotencyKeyLengthLimit = 128
)
