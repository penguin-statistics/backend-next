package logger

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

func TestConfigureLevel(t *testing.T) {
	// Configure creates a logs directory in the working directory
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	prev := log.Logger
	t.Cleanup(func() {
		log.Logger = prev
		if err := os.Chdir(wd); err != nil {
			t.Error(err)
		}
	})

	for _, tc := range []struct {
		devMode bool
		want    zerolog.Level
	}{
		{false, zerolog.InfoLevel},
		{true, zerolog.TraceLevel},
	} {
		Configure(&appconfig.Config{ConfigSpec: appconfig.ConfigSpec{DevMode: tc.devMode, LogJsonStdout: true}})
		if got := log.Logger.GetLevel(); got != tc.want {
			t.Errorf("DevMode %v: level %v, want %v", tc.devMode, got, tc.want)
		}
	}
}
