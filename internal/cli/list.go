package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/state"
)

// backupInfo is the `list backups --json` element.
type backupInfo struct {
	ID        string    `json:"id"`
	Job       string    `json:"job"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Created   time.Time `json:"created"`
	RunID     string    `json:"run_id"`
	FileCount int64     `json:"file_count"`
	Bytes     int64     `json:"bytes"`
}

func listCmd() *command {
	return &command{
		name:    "list",
		summary: "List jobs, runs or backups",
		subs:    []*command{listJobsCmd(), listRunsCmd(), listBackupsCmd()},
	}
}

func listJobsCmd() *command {
	return &command{
		name: "jobs", summary: "List defined jobs", usage: "jobs [--json]", nargs: 0,
		setup: func(a *app, fs *flagSet) runFunc {
			asJSON := fs.Bool("json", false, "machine-readable JSON output")
			return func([]string) error { return a.listJobs(*asJSON) }
		},
	}
}

func (a *app) listJobs(asJSON bool) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	js, errs := store.ListJobs()
	a.warn(errs)
	if asJSON {
		return writeJSON(a.out(), orEmpty(js))
	}
	if len(js) == 0 {
		_, _ = fmt.Fprintln(a.out(), "no jobs")
		return nil
	}
	tw := newTable(a.out())
	_, _ = fmt.Fprintln(tw, "NAME\tSCHEDULE\tKEEP\tSCHEDULED\tSOURCE\tDESTINATION")
	for _, j := range js {
		sch := j.Schedule
		if sch == "" {
			sch = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%v\t%s\t%s\n", j.Name, sch, j.Keep, j.Scheduled, j.Source, j.DstTemplate)
	}
	return tw.Flush()
}

func listRunsCmd() *command {
	return &command{
		name: "runs", summary: "List recorded runs, newest first", usage: "runs [--job NAME] [--json]", nargs: 0,
		setup: func(a *app, fs *flagSet) runFunc {
			asJSON := fs.Bool("json", false, "machine-readable JSON output")
			jobFilter := fs.String("job", "", "only runs of this job")
			return func([]string) error { return a.listRuns(*jobFilter, *asJSON) }
		},
	}
}

func (a *app) listRuns(jobFilter string, asJSON bool) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	rs, errs := store.ListRuns(jobFilter)
	a.warn(errs)
	if asJSON {
		return writeJSON(a.out(), orEmpty(rs))
	}
	if len(rs) == 0 {
		_, _ = fmt.Fprintln(a.out(), "no runs")
		return nil
	}
	return printRuns(a.out(), rs)
}

func printRuns(w io.Writer, rs []state.Status) error {
	tw := newTable(w)
	_, _ = fmt.Fprintln(tw, "RUN ID\tJOB\tSTATE\tSTARTED\tDURATION\tEXIT")
	for _, r := range rs {
		dur := "-"
		if !r.Finished.IsZero() {
			dur = r.Finished.Sub(r.Started).Round(time.Second).String()
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n", r.RunID, r.Job, r.State, r.Started.Format(time.RFC3339), dur, r.ExitCode)
	}
	return tw.Flush()
}

func listBackupsCmd() *command {
	return &command{
		name: "backups", summary: "List committed backups found on disk", usage: "backups [--job NAME] [--json]", nargs: 0,
		setup: func(a *app, fs *flagSet) runFunc {
			asJSON := fs.Bool("json", false, "machine-readable JSON output")
			jobFilter := fs.String("job", "", "only backups of this job")
			return func([]string) error { return a.listBackups(*jobFilter, *asJSON) }
		},
	}
}

func (a *app) listBackups(jobFilter string, asJSON bool) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	infos := a.collectBackups(store, jobFilter)
	if asJSON {
		return writeJSON(a.out(), infos)
	}
	if len(infos) == 0 {
		_, _ = fmt.Fprintln(a.out(), "no backups")
		return nil
	}
	tw := newTable(a.out())
	_, _ = fmt.Fprintln(tw, "ID\tCREATED\tFILES\tSIZE\tPATH")
	for _, i := range infos {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", i.ID, i.Created.Format(time.RFC3339), i.FileCount, HumanBytes(i.Bytes), i.Path)
	}
	return tw.Flush()
}

// collectBackups scans every (or one) job's destination parent for committed backups.
func (a *app) collectBackups(store state.Store, jobFilter string) []backupInfo {
	js, errs := store.ListJobs()
	a.warn(errs)
	infos := []backupInfo{}
	for _, j := range js {
		if jobFilter != "" && j.Name != jobFilter {
			continue
		}
		all, err := backupset.Scan(j)
		if err != nil {
			a.warn([]error{fmt.Errorf("job %s: %w", j.Name, err)})
			continue
		}
		for _, b := range backupset.OnlyCommitted(all) {
			infos = append(infos, backupInfo{
				ID: b.ID(), Job: j.Name, Name: b.Name, Path: b.Path,
				Created: b.Marker.Created, RunID: b.Marker.RunID,
				FileCount: b.Marker.FileCount, Bytes: b.Marker.Bytes,
			})
		}
	}
	return infos
}

func newTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

func (a *app) warn(errs []error) {
	for _, e := range errs {
		_, _ = fmt.Fprintf(a.err(), "warning: %v\n", e)
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	return nil
}

// orEmpty makes nil slices encode as [] rather than null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// HumanBytes formats a byte count with binary units.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
