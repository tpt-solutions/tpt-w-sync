// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Command tpt-sync is the command-line interface to the tpt-sync engine.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/tpt-solutions/tpt-sync/engine"
	"github.com/tpt-solutions/tpt-sync/engine/daemon"
	"github.com/tpt-solutions/tpt-sync/engine/ipc"
	"github.com/tpt-solutions/tpt-sync/engine/schedule"
	"github.com/tpt-solutions/tpt-sync/engine/secrets"
)

const usage = `tpt-sync — versioned backup & sync

Usage:
  tpt-sync [--config DIR] <command> [args]

Commands:
  job add <name> [flags]                           create a job
  job edit <name> [flags]                          change a job (only given flags)
  job list                                         list jobs
  job show <name>                                  show one job as JSON
  job remove <name>                                delete a job
  run <name> [--dry-run]                           run a job now
  schedule enable <name>                           let the scheduler run a job
  schedule disable <name>                          stop automatic runs of a job
  versions list <name>                             list snapshots
  versions restore <name> [--version TS] [--path REL] --to DIR
  secret set <name> passphrase|password            store a secret in the OS keychain
  secret delete <name> passphrase|password         remove a stored secret
  status                                           last run and next scheduled run per job
  daemon [--bandwidth-limit N]                     run scheduler in the foreground

Destination flags (job add/edit):
  --type local|webdav|s3   destination kind (inferred from --url/--bucket if omitted)
  --dest DIR               local directory, or folder within a remote
  --url URL                WebDAV endpoint, or S3 endpoint (empty = AWS)
  --bucket NAME            S3 bucket
  --region NAME            S3 region
  --username NAME          WebDAV user, or S3 access key ID
  --encrypt                end-to-end encrypt (passphrase from keychain)

Other job flags:
  --source DIR        directory to back up
  --include PATTERN   (repeatable) only sync matching paths
  --exclude PATTERN   (repeatable) skip matching paths
  --max-versions N    keep at most N snapshots (0 = unlimited)
  --max-age DURATION  delete snapshots older than this, e.g. 720h
  --concurrency N     parallel transfers (default 4)
  --schedule EXPR     cron expression, e.g. "0 2 * * *" or @daily
  --watch             also run when source files change

Passphrases: stored via "secret set". TPT_SYNC_PASSPHRASE overrides the
stored passphrase (useful for scripts; visible to your other processes).
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "tpt-sync:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var configDir string
	if len(args) >= 2 && args[0] == "--config" {
		configDir = args[1]
		args = args[2:]
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return nil
	}
	if configDir == "" {
		configDir = os.Getenv("TPT_SYNC_CONFIG")
	}
	if configDir == "" {
		d, err := engine.DefaultConfigDir()
		if err != nil {
			return err
		}
		configDir = d
	}
	c := &cli{store: engine.Store{Dir: configDir}, out: stdout, errOut: stderr}

	switch args[0] {
	case "job":
		return c.job(args[1:])
	case "run":
		return c.runCmd(args[1:])
	case "schedule":
		return c.schedule(args[1:])
	case "versions":
		return c.versions(args[1:])
	case "status":
		return c.status()
	case "daemon":
		return c.daemonCmd(args[1:])
	case "secret":
		return c.secretCmd(args[1:])
	default:
		return fmt.Errorf("unknown command %q (try --help)", args[0])
	}
}

type cli struct {
	store  engine.Store
	out    io.Writer
	errOut io.Writer
}

// secretStore is the store used for passphrases and destination passwords.
func (c *cli) secretStore() secrets.Store {
	return secrets.EnvOverride{Next: secrets.Keychain{}}
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// jobFlags binds the flags shared by "job add" and "job edit".
type jobFlags struct {
	fs          *flag.FlagSet
	source      string
	typ         string
	dest        string
	url         string
	bucket      string
	region      string
	username    string
	include     stringList
	exclude     stringList
	maxVersions int
	maxAge      time.Duration
	concurrency int
	schedule    string
	watch       bool
	encrypt     bool
	bandwidth   string
	missedRun   string
}

func newJobFlags(name string, errOut io.Writer) *jobFlags {
	j := &jobFlags{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	j.fs.SetOutput(errOut)
	j.fs.StringVar(&j.source, "source", "", "source directory")
	j.fs.StringVar(&j.typ, "type", "", "destination type: local, webdav or s3")
	j.fs.StringVar(&j.dest, "dest", "", "local directory, or folder within a remote")
	j.fs.StringVar(&j.url, "url", "", "WebDAV or S3 endpoint URL")
	j.fs.StringVar(&j.bucket, "bucket", "", "S3 bucket")
	j.fs.StringVar(&j.region, "region", "", "S3 region")
	j.fs.StringVar(&j.username, "username", "", "WebDAV user or S3 access key ID")
	j.fs.Var(&j.include, "include", "include pattern (repeatable)")
	j.fs.Var(&j.exclude, "exclude", "exclude pattern (repeatable)")
	j.fs.IntVar(&j.maxVersions, "max-versions", 0, "max snapshots to keep (0 = unlimited)")
	j.fs.DurationVar(&j.maxAge, "max-age", 0, "max snapshot age (0 = unlimited)")
	j.fs.IntVar(&j.concurrency, "concurrency", 0, "parallel transfers")
	j.fs.StringVar(&j.schedule, "schedule", "", "cron expression")
	j.fs.BoolVar(&j.watch, "watch", false, "run on filesystem changes")
	j.fs.StringVar(&j.bandwidth, "bandwidth-limit", "", "upload cap per second, e.g. 5M (0 or empty = unlimited)")
	j.fs.StringVar(&j.missedRun, "missed-run", "", "catch-up (run once after a missed schedule) or skip")
	j.fs.BoolVar(&j.encrypt, "encrypt", false, "end-to-end encrypt the destination (passphrase via TPT_SYNC_PASSPHRASE)")
	return j
}

// apply copies flags that were explicitly set onto job. set reports which
// flag names were given on the command line.
func (j *jobFlags) apply(job *engine.Job, set map[string]bool) error {
	if set["missed-run"] {
		job.MissedRun = j.missedRun
	}
	if set["bandwidth-limit"] {
		n, err := engine.ParseBytes(j.bandwidth)
		if err != nil {
			return err
		}
		job.BandwidthLimit = n
	}
	if set["source"] {
		job.Source = j.source
	}
	destFlags := []string{"type", "dest", "url", "bucket", "region", "username"}
	for _, f := range destFlags {
		if set[f] {
			j.applyDestination(&job.Destination, set)
			break
		}
	}
	if set["include"] {
		job.Filters.Include = append([]string(nil), j.include...)
	}
	if set["exclude"] {
		job.Filters.Exclude = append([]string(nil), j.exclude...)
	}
	if set["max-versions"] {
		job.Versioning.MaxVersions = j.maxVersions
	}
	if set["max-age"] {
		job.Versioning.MaxAge = j.maxAge
	}
	if set["concurrency"] {
		job.Concurrency = j.concurrency
	}
	if set["schedule"] {
		job.Schedule = j.schedule
	}
	if set["watch"] {
		job.Watch = j.watch
	}
	if set["encrypt"] {
		job.Encrypt = j.encrypt
	}
	return nil
}

// applyDestination updates only the destination fields whose flags were given.
// When no type is known yet, it is inferred: bucket → s3, url → webdav, else local.
func (j *jobFlags) applyDestination(d *engine.Destination, set map[string]bool) {
	if set["type"] {
		d.Type = j.typ
	}
	if set["dest"] {
		d.Path = j.dest
	}
	if set["url"] {
		d.URL = j.url
	}
	if set["bucket"] {
		d.Bucket = j.bucket
	}
	if set["region"] {
		d.Region = j.region
	}
	if set["username"] {
		d.Username = j.username
	}
	if d.Type == "" {
		switch {
		case d.Bucket != "" || set["bucket"]:
			d.Type = "s3"
		case d.URL != "" || set["url"]:
			d.Type = "webdav"
		default:
			d.Type = "local"
		}
	}
}

// destinationDesc is a short human-readable description for listings.
func destinationDesc(d engine.Destination) string {
	switch d.Type {
	case "s3":
		return "s3://" + d.Bucket + "/" + strings.Trim(d.Path, "/")
	case "webdav":
		if p := strings.Trim(d.Path, "/"); p != "" {
			return d.URL + "/" + p
		}
		return d.URL
	default:
		return d.Path
	}
}

func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func (c *cli) job(args []string) error {
	if len(args) == 0 {
		return errors.New("job: need a subcommand (add, edit, list, show, remove)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return c.jobAdd(rest)
	case "edit":
		return c.jobEdit(rest)
	case "list":
		return c.jobList()
	case "show":
		return c.jobShow(rest)
	case "remove":
		return c.jobRemove(rest)
	default:
		return fmt.Errorf("job: unknown subcommand %q", sub)
	}
}

// splitName takes the job name as the first argument; flags follow it.
func splitName(args []string) (string, []string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, errors.New("missing job name")
	}
	return args[0], args[1:], nil
}

func (c *cli) jobAdd(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	jf := newJobFlags("job add", c.errOut)
	if err := jf.fs.Parse(rest); err != nil {
		return err
	}
	job := engine.Job{Name: name, Enabled: true}
	if err := jf.apply(&job, setFlags(jf.fs)); err != nil {
		return err
	}
	jobs, err := c.store.LoadJobs()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Name == name {
			return fmt.Errorf("job %q already exists", name)
		}
	}
	if err := c.store.SaveJobs(append(jobs, job)); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "added job %s\n", name)
	return nil
}

func (c *cli) jobEdit(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	jf := newJobFlags("job edit", c.errOut)
	if err := jf.fs.Parse(rest); err != nil {
		return err
	}
	jobs, err := c.store.LoadJobs()
	if err != nil {
		return err
	}
	found := false
	for i := range jobs {
		if jobs[i].Name == name {
			if err := jf.apply(&jobs[i], setFlags(jf.fs)); err != nil {
				return err
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no such job %q", name)
	}
	if err := c.store.SaveJobs(jobs); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "updated job %s\n", name)
	return nil
}

func (c *cli) jobList() error {
	jobs, err := c.store.LoadJobs()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENABLED\tSOURCE\tDESTINATION\tSCHEDULE\tWATCH")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%t\t%s\t%s\t%s\t%t\n", j.Name, j.Enabled, j.Source,
			destinationDesc(j.Destination), dashIfEmpty(j.Schedule), j.Watch)
	}
	return tw.Flush()
}

func (c *cli) jobShow(args []string) error {
	name, _, err := splitName(args)
	if err != nil {
		return err
	}
	job, err := c.store.Job(name)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, string(b))
	return nil
}

func (c *cli) jobRemove(args []string) error {
	name, _, err := splitName(args)
	if err != nil {
		return err
	}
	jobs, err := c.store.LoadJobs()
	if err != nil {
		return err
	}
	kept := jobs[:0]
	removed := false
	for _, j := range jobs {
		if j.Name == name {
			removed = true
			continue
		}
		kept = append(kept, j)
	}
	if !removed {
		return fmt.Errorf("no such job %q", name)
	}
	if err := c.store.SaveJobs(kept); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "removed job %s (destination snapshots are left in place)\n", name)
	return nil
}

func (c *cli) runCmd(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	dry := fs.Bool("dry-run", false, "show the plan without writing anything")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	job, err := c.store.Job(name)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := engine.Options{DryRun: *dry, Secrets: c.secretStore()}
	if !*dry {
		opts.OnProgress = func(p engine.Progress) {
			if p.Err != nil {
				fmt.Fprintf(c.errOut, "  [%d/%d] FAILED %s: %v\n", p.Done, p.Total, p.Action.Path, p.Err)
			}
		}
	}
	if !*dry {
		if handled, err := c.runViaDaemon(ctx, name); handled {
			return err
		}
	}
	started := time.Now()
	res, err := engine.Run(ctx, job, opts)
	if !*dry {
		rec := engine.RunRecord{
			Started: started, Finished: time.Now(), Snapshot: res.Snapshot, OK: err == nil,
			Copied: res.Copied, Linked: res.Linked, Deleted: res.Deleted,
		}
		if err != nil {
			rec.Error = err.Error()
		}
		if serr := c.store.RecordRun(name, rec); serr != nil {
			fmt.Fprintln(c.errOut, "tpt-sync: warning: recording run:", serr)
		}
	}
	if err != nil {
		return err
	}
	if *dry {
		for _, a := range res.Plan.Actions {
			if a.Kind == engine.KindUnchanged {
				continue
			}
			fmt.Fprintf(c.out, "%-9s %s  (%s)\n", a.Kind, a.Path, a.Reason)
		}
		cnt := res.Plan.Counts()
		fmt.Fprintf(c.out, "dry run: %d create, %d update, %d unchanged, %d delete\n",
			cnt[engine.KindCreate], cnt[engine.KindUpdate], cnt[engine.KindUnchanged], cnt[engine.KindDelete])
		return nil
	}
	fmt.Fprintf(c.out, "snapshot %s: %d copied, %d linked, %d removed from latest\n",
		res.Snapshot, res.Copied, res.Linked, res.Deleted)
	return nil
}

// endpoint is the control channel of the daemon that owns this config directory.
func (c *cli) endpoint() string { return ipc.DefaultEndpoint(c.store.Dir) }

// runViaDaemon asks a running daemon to perform the run, so manual and
// scheduled runs never overlap. handled is false when no daemon is running and
// the caller should run in-process.
func (c *cli) runViaDaemon(ctx context.Context, name string) (handled bool, err error) {
	resp, err := ipc.Call(ctx, c.endpoint(), ipc.Request{Op: "run", Job: name})
	if errors.Is(err, ipc.ErrNotRunning) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if !resp.OK {
		return true, errors.New(resp.Error)
	}
	var rec engine.RunRecord
	if err := json.Unmarshal(resp.Data, &rec); err != nil {
		return true, fmt.Errorf("daemon reply: %w", err)
	}
	fmt.Fprintf(c.out, "snapshot %s (run by daemon): %d copied, %d linked, %d removed from latest\n",
		rec.Snapshot, rec.Copied, rec.Linked, rec.Deleted)
	return true, nil
}

func (c *cli) schedule(args []string) error {
	if len(args) != 2 || (args[0] != "enable" && args[0] != "disable") {
		return errors.New("usage: schedule enable|disable <name>")
	}
	jobs, err := c.store.LoadJobs()
	if err != nil {
		return err
	}
	found := false
	for i := range jobs {
		if jobs[i].Name == args[1] {
			jobs[i].Enabled = args[0] == "enable"
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no such job %q", args[1])
	}
	if err := c.store.SaveJobs(jobs); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%sd automatic runs for %s\n", args[0], args[1])
	return nil
}

func (c *cli) versions(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: versions list|restore <name> ...")
	}
	sub, name, rest := args[0], args[1], args[2:]
	job, err := c.store.Job(name)
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		dst, err := engine.OpenDestination(job, c.secretStore())
		if err != nil {
			return err
		}
		snaps, err := engine.ListSnapshots(context.Background(), dst)
		if err != nil {
			return err
		}
		latest, _ := engine.ReadLatest(context.Background(), dst)
		for _, s := range snaps {
			mark := ""
			if s == latest {
				mark = "  (latest)"
			}
			fmt.Fprintf(c.out, "%s%s\n", s, mark)
		}
		if len(snaps) == 0 {
			fmt.Fprintln(c.out, "no snapshots")
		}
		return nil
	case "restore":
		fs := flag.NewFlagSet("versions restore", flag.ContinueOnError)
		fs.SetOutput(c.errOut)
		ver := fs.String("version", "", "snapshot name (default: latest)")
		p := fs.String("path", "", "file or folder within the snapshot (default: all)")
		to := fs.String("to", "", "directory to restore into (required)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *to == "" {
			return errors.New("--to is required")
		}
		n, err := engine.Restore(context.Background(), job,
			engine.RestoreOptions{Snapshot: *ver, Path: *p, Secrets: c.secretStore()}, *to)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "restored %d file(s) to %s\n", n, *to)
		return nil
	default:
		return fmt.Errorf("versions: unknown subcommand %q", sub)
	}
}

// secretCmd implements "secret set|delete <job> passphrase|password".
func (c *cli) secretCmd(args []string) error {
	if len(args) != 3 || (args[0] != "set" && args[0] != "delete") {
		return errors.New("usage: secret set|delete <job> passphrase|password")
	}
	action, jobName, kind := args[0], args[1], args[2]
	if _, err := c.store.Job(jobName); err != nil {
		return err
	}
	var account string
	switch kind {
	case "passphrase":
		account = secrets.PassphraseAccount(jobName)
	case "password":
		account = secrets.PasswordAccount(jobName)
	default:
		return fmt.Errorf("unknown secret %q (want passphrase or password)", kind)
	}
	kc := secrets.Keychain{}
	if action == "delete" {
		if err := kc.Delete(account); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "deleted stored %s for %s\n", kind, jobName)
		return nil
	}

	value, err := readSecret(fmt.Sprintf("%s for %s: ", kind, jobName), c.errOut)
	if err != nil {
		return err
	}
	if value == "" {
		return errors.New("empty value; nothing stored")
	}
	if kind == "passphrase" {
		again, err := readSecret("confirm passphrase: ", c.errOut)
		if err != nil {
			return err
		}
		if again != value {
			return errors.New("passphrases do not match; nothing stored")
		}
		fmt.Fprintln(c.errOut, "WARNING: an encrypted backup cannot be read without this passphrase.")
		fmt.Fprintln(c.errOut, "Record it somewhere safe outside this computer. Losing it means losing the backups.")
	}
	if err := kc.Set(account, value); err != nil {
		return fmt.Errorf("storing secret in OS keychain: %w", err)
	}
	fmt.Fprintf(c.out, "stored %s for %s\n", kind, jobName)
	return nil
}

// readSecret reads one line from the terminal without echo, or from piped stdin.
func readSecret(prompt string, errOut io.Writer) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(errOut, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(errOut)
		return string(b), err
	}
	var line strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			if buf[0] != '\r' {
				line.WriteByte(buf[0])
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
	}
	return line.String(), nil
}

func (c *cli) status() error {
	jobs, err := c.store.LoadJobs()
	if err != nil {
		return err
	}
	runs, err := c.store.LastRuns()
	if err != nil {
		return err
	}
	// Ask the daemon for live state if one is running.
	live := map[string]daemon.JobStatus{}
	daemonUp := false
	if resp, err := ipc.Call(context.Background(), c.endpoint(), ipc.Request{Op: "status"}); err == nil && resp.OK {
		var statuses []daemon.JobStatus
		if json.Unmarshal(resp.Data, &statuses) == nil {
			daemonUp = true
			for _, s := range statuses {
				live[s.Name] = s
			}
		}
	}
	tw := tabwriter.NewWriter(c.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENABLED\tRUNNING\tLAST RUN\tRESULT\tNEXT RUN")
	for _, j := range jobs {
		last, result := "never", "-"
		if r, ok := runs[j.Name]; ok {
			last = r.Started.Local().Format("2006-01-02 15:04")
			result = "ok"
			if !r.OK {
				result = "failed: " + r.Error
			}
		}
		next := "-"
		if j.Enabled && j.Schedule != "" {
			if s, err := schedule.Parse(j.Schedule); err == nil {
				if n := s.Next(time.Now()); !n.IsZero() {
					next = n.Local().Format("2006-01-02 15:04")
				}
			}
		}
		if j.Enabled && j.Watch {
			if next == "-" {
				next = "on change"
			} else {
				next += " (+ on change)"
			}
		}
		running := "-"
		if daemonUp {
			running = "no"
			if live[j.Name].Running {
				running = "yes"
			}
		}
		fmt.Fprintf(tw, "%s\t%t\t%s\t%s\t%s\t%s\n", j.Name, j.Enabled, running, last, result, next)
	}
	return tw.Flush()
}

func (c *cli) daemonCmd(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	bw := fs.String("bandwidth-limit", "", "upload cap across all jobs, e.g. 10M per second (empty = unlimited)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	limit, err := engine.ParseBytes(*bw)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d := &daemon.Daemon{
		Store: c.store, Log: c.out, Secrets: c.secretStore(),
		ControlEndpoint: c.endpoint(), Bandwidth: limit,
	}
	return d.Run(ctx)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
