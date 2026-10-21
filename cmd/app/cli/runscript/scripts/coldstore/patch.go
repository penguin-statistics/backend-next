package script_coldstore

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/coldstore"
)

func runPatch(ctx *cli.Context, deps CommandDeps) error {
	var file io.Reader
	if name := ctx.String("file"); name != "" {
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		file = f
	}
	p, err := parsePatch(ctx.StringSlice("delete"), ctx.StringSlice("reliability"), file)
	if err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}

	dir := ctx.String("dir")
	var months []string
	if key := ctx.String("month"); key != "" {
		if _, err := coldstore.ParseMonth(key); err != nil {
			return err
		}
		months = []string{key}
	}
	dryRun := ctx.Bool("dry-run")
	// a patch that stopped partway is completed or undone before the reports are located: a delete patch that
	// stopped after its commit would otherwise find its reports in no month, and leave the month half swapped
	recovered, err := coldstore.PreparePatch(dir, months, dryRun)
	for key, rec := range recovered {
		if rec != coldstore.RecoveryNone {
			log.Warn().Str("month", key).Str("recovery", string(rec)).Msg("coldstore: recovered a patch that had stopped partway")
		}
	}
	if err != nil {
		return err
	}
	manifest := filepath.Join(dir, coldstore.ManifestFile)
	m, err := coldstore.ReadManifest(manifest)
	if err != nil {
		return err
	}
	opts := options(ctx, deps)
	started := time.Now()

	// find the months first, so that a wrong id changes nothing
	ids := p.IDs()
	found, err := coldstore.LocateReports(ctx.Context, opts, dir, m, months, ids)
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, f := range found {
		for _, id := range f {
			seen[id] = true
		}
	}
	var missing []string
	for _, id := range ids {
		if !seen[id] {
			missing = append(missing, strconv.FormatInt(id, 10))
		}
	}
	if len(missing) > 0 {
		ev := log.Warn()
		if !ctx.Bool("ignore-missing") {
			ev = log.Error()
		}
		ev.Int("missing", len(missing)).Str("ids", truncate(strings.Join(missing, ","), 1000)).
			Msg("coldstore: reports in no archived month")
		if !ctx.Bool("ignore-missing") {
			// a delete patch that stopped after some months finds their reports gone
			return fmt.Errorf("%d of %d reports are in no archived month (or deleted by an earlier run of this patch); "+
				"pass --ignore-missing to patch the others, or --month for the months not patched yet", len(missing), len(ids))
		}
	}

	for _, e := range m.Months {
		monthIDs, ok := found[e.Month]
		if !ok {
			continue
		}
		month, err := coldstore.ParseMonth(e.Month)
		if err != nil {
			return err
		}
		// PatchMonth records the month's new files in the manifest itself, recoverably
		if _, err := coldstore.PatchMonth(ctx.Context, opts, dir, month, p.Only(monthIDs), dryRun); err != nil {
			return err
		}
	}
	log.Info().Int("months", len(found)).Int("reports", len(ids)-len(missing)).Bool("dryRun", dryRun).
		Dur("took", time.Since(started)).Msg("coldstore: patch finished")
	return nil
}

func runRecover(ctx *cli.Context) error {
	dir := ctx.String("dir")
	var months []string
	if key := ctx.String("month"); key != "" {
		if _, err := coldstore.ParseMonth(key); err != nil {
			return err
		}
		months = []string{key}
	}
	done, err := coldstore.RecoverPatches(dir, months)
	for key, rec := range done {
		if rec == coldstore.RecoveryNone {
			log.Info().Str("month", key).Msg("coldstore: nothing to recover")
		} else {
			log.Warn().Str("month", key).Str("recovery", string(rec)).Msg("coldstore: recovered a patch that had stopped partway")
		}
	}
	if err != nil {
		return err
	}
	log.Info().Int("months", len(done)).Msg("coldstore: recovery finished")
	return nil
}

// parsePatch reads the changes of the --delete and --reliability flags and of the --file lines ('<report_id>
// <reliability>' or '<report_id> delete', blank lines and '#' comments ignored).
func parsePatch(deletes, reliabilities []string, file io.Reader) (coldstore.Patch, error) {
	p := coldstore.Patch{Reliability: map[int64]int{}}
	setReliability := func(id int64, r int) error {
		if _, ok := p.Reliability[id]; ok {
			return fmt.Errorf("report %d is given a reliability twice", id)
		}
		p.Reliability[id] = r
		return nil
	}
	for _, s := range deletes {
		id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return p, fmt.Errorf("invalid report id %q in --delete", s)
		}
		p.Delete = append(p.Delete, id)
	}
	for _, s := range reliabilities {
		k, v, ok := strings.Cut(s, "=")
		id, err1 := strconv.ParseInt(strings.TrimSpace(k), 10, 64)
		r, err2 := strconv.Atoi(strings.TrimSpace(v))
		if !ok || err1 != nil || err2 != nil {
			return p, fmt.Errorf("invalid --reliability %q, want report_id=reliability", s)
		}
		if err := setReliability(id, r); err != nil {
			return p, err
		}
	}
	if file == nil {
		return p, nil
	}
	sc := bufio.NewScanner(file)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(strings.ReplaceAll(line, ",", " "))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return p, fmt.Errorf("line %d: want '<report_id> <reliability>' or '<report_id> delete'", n)
		}
		id, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return p, fmt.Errorf("line %d: invalid report id %q", n, fields[0])
		}
		if fields[1] == "delete" {
			p.Delete = append(p.Delete, id)
			continue
		}
		r, err := strconv.Atoi(fields[1])
		if err != nil {
			return p, fmt.Errorf("line %d: invalid reliability %q", n, fields[1])
		}
		if err := setReliability(id, r); err != nil {
			return p, fmt.Errorf("line %d: %w", n, err)
		}
	}
	return p, errors.Wrap(sc.Err(), "read changes")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
