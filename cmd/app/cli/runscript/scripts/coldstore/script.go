package script_coldstore

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/coldstore"
)

func options(ctx *cli.Context, deps CommandDeps) coldstore.Options {
	return coldstore.Options{
		PostgresDSN:        deps.Config.PostgresDSN,
		ExtensionDirectory: deps.Config.ColdstoreExtensionDirectory,
		WorkDir:            ctx.String("work-dir"),
		Threads:            ctx.Int("threads"),
		MemoryLimit:        ctx.String("memory-limit"),
	}
}

func runExport(ctx *cli.Context, deps CommandDeps, monthKey string, dims bool, out string) error {
	if monthKey == "" && !dims {
		return errors.New("nothing to export: pass --month and/or --dims")
	}
	opts := options(ctx, deps)
	manifest := filepath.Join(out, coldstore.ManifestFile)
	started := time.Now()

	if monthKey != "" {
		month, err := coldstore.ParseMonth(monthKey)
		if err != nil {
			return err
		}
		if grace := ctx.Duration("grace"); !month.Over(time.Now(), grace) {
			return fmt.Errorf("month %s is not over: it ended less than --grace %v ago, and may still gain reports", month.Key, grace)
		}
		// the month's lock is held from the check of the manifest to its update: an export of the same month
		// running alongside waits, and finds the month archived
		err = coldstore.Publish(out, coldstore.MonthTarget(month.Key), func(m *coldstore.Manifest) (func(*coldstore.Manifest), error) {
			// an archived month may be gone from PostgreSQL: exporting it again would replace the only copy
			archived := m.Month(month.Key)
			var entry *coldstore.MonthEntry
			var err error
			switch {
			case archived == nil:
				entry, err = coldstore.ExportMonth(ctx.Context, opts, month, out)
			case !ctx.Bool("replace"):
				return nil, fmt.Errorf("month %s is already archived in %s; pass --replace to export it again from PostgreSQL, "+
					"only while PostgreSQL still holds the whole month", month.Key, manifest)
			case ctx.Bool("allow-fewer-rows"):
				entry, err = coldstore.ExportMonth(ctx.Context, opts, month, out)
			default:
				entry, err = coldstore.ReexportMonth(ctx.Context, opts, month, out, archived)
			}
			if err != nil {
				return nil, err
			}
			return func(m *coldstore.Manifest) { m.PutMonth(*entry) }, nil
		})
		if err != nil {
			return err
		}
	}
	if dims {
		err := coldstore.Publish(out, coldstore.DimsTarget, func(m *coldstore.Manifest) (func(*coldstore.Manifest), error) {
			entry, err := coldstore.ExportDims(ctx.Context, opts, out, m.Dim(coldstore.DropPatternElementsFile))
			if err != nil {
				return nil, err
			}
			return func(m *coldstore.Manifest) { m.PutDim(*entry) }, nil
		})
		if err != nil {
			return err
		}
	}

	log.Info().Str("month", monthKey).Bool("dims", dims).Str("manifest", manifest).Dur("took", time.Since(started)).
		Msg("coldstore: export finished")
	return nil
}

func runVerify(ctx *cli.Context, deps CommandDeps, monthKey string, dims bool, dir, manifest string, againstSource bool) error {
	if monthKey == "" && !dims {
		return errors.New("nothing to verify: pass --month and/or --dims")
	}
	if manifest == "" {
		manifest = filepath.Join(dir, coldstore.ManifestFile)
	}
	m, err := coldstore.ReadManifest(manifest)
	if err != nil {
		return err
	}
	opts := options(ctx, deps)
	started := time.Now()

	if monthKey != "" {
		month, err := coldstore.ParseMonth(monthKey)
		if err != nil {
			return err
		}
		if err := coldstore.VerifyMonth(ctx.Context, opts, dir, m, month, againstSource); err != nil {
			return err
		}
	}
	if dims {
		if err := coldstore.VerifyDims(ctx.Context, opts, dir, m, againstSource); err != nil {
			return err
		}
	}

	log.Info().Str("month", monthKey).Bool("dims", dims).Bool("againstSource", againstSource).
		Dur("took", time.Since(started)).Msg("coldstore: verification passed")
	return nil
}

func runManifest(ctx *cli.Context, deps CommandDeps, dir, out string, force bool) error {
	own := filepath.Join(dir, coldstore.ManifestFile)
	started := time.Now()
	var m *coldstore.Manifest
	var err error
	if out == "" || sameFile(out, own) {
		// the directory's own manifest: exports and patches write it too
		out = own
		m, err = coldstore.RebuildManifest(ctx.Context, options(ctx, deps), dir, force)
		if err != nil {
			return err
		}
	} else {
		m, err = coldstore.BuildManifest(ctx.Context, options(ctx, deps), dir)
		if err != nil {
			return err
		}
		if err := coldstore.WriteManifest(out, m); err != nil {
			return err
		}
	}
	log.Info().Int("months", len(m.Months)).Str("manifest", out).Dur("took", time.Since(started)).
		Msg("coldstore: manifest written")
	return nil
}

// sameFile tells whether two paths name the same file: the same absolute path, or the same existing file.
func sameFile(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil && absA == absB {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
