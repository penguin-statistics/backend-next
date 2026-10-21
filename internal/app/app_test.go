package app

import (
	"testing"

	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appcontext"
)

// TestOptionsGraphIsComplete checks that every constructor and invoke in the server's fx graph has its dependencies
// provided. The compiler cannot see a missing fx provider, so removing one that something still needs only fails at
// startup otherwise. fx.ValidateApp runs no constructors, so this needs no database, Redis or NATS.
func TestOptionsGraphIsComplete(t *testing.T) {
	// the required configuration keys that have no default; nothing connects anywhere
	t.Setenv("PENGUIN_V3_POSTGRES_DSN", "postgres://127.0.0.1:1/unused?sslmode=disable")
	t.Setenv("PENGUIN_V3_DROP_REPORT_ARCHIVE_S3_BUCKET", "unused")
	t.Setenv("PENGUIN_V3_DROP_REPORT_ARCHIVE_S3_REGION", "unused")
	t.Setenv("PENGUIN_V3_AWS_ACCESS_KEY", "unused")
	t.Setenv("PENGUIN_V3_AWS_SECRET_KEY", "unused")

	if err := fx.ValidateApp(Options(appcontext.Declare(appcontext.EnvServer))...); err != nil {
		t.Fatalf("fx graph is incomplete: %v", err)
	}
}
