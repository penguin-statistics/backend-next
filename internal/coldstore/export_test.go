package coldstore

// The helpers of archive_integration_test.go used by archive_recall_integration_test.go (package coldstore_test).
var (
	NewTestData     = newTestData
	NewTestNewDB    = newTestNewDB
	NewFakeBucket   = newFakeBucket
	NewTestArchiver = newTestArchiver
	SetArchiveHook  = setArchiveHook
)
