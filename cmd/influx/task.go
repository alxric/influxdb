package main

import (
	"context"
	"fmt"
	"github.com/influxdata/influxdb/v2/tenant"
	"io"
	"os"
	"time"

	"github.com/influxdata/influxdb/v2"
	"github.com/influxdata/influxdb/v2/cmd/influx/internal"
	"github.com/influxdata/influxdb/v2/http"
	"github.com/spf13/cobra"
)

type taskSVCsFn func() (influxdb.TaskService, influxdb.OrganizationService, error)

func newTaskSVCs() (influxdb.TaskService, influxdb.OrganizationService, error) {
	httpClient, err := newHTTPClient()
	if err != nil {
		return nil, nil, err
	}

	orgSvc := &tenant.OrgClientService{Client: httpClient}
	return &http.TaskService{Client: httpClient}, orgSvc, nil
}

func cmdTask(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	builder := newCmdTaskBuilder(newTaskSVCs, f, opt)
	return builder.cmd()
}

type cmdTaskBuilder struct {
	genericCLIOpts
	*globalFlags

	svcFn taskSVCsFn

	id          string
	hideHeaders bool
	json        bool
	name        string
	description string
	org         organization
	query       string
}

func newCmdTaskBuilder(svcsFn taskSVCsFn, f *globalFlags, opts genericCLIOpts) *cmdTaskBuilder {
	return &cmdTaskBuilder{
		globalFlags:    f,
		genericCLIOpts: opts,
		svcFn:          svcsFn,
	}
}

func (t *cmdTaskBuilder) cmd() *cobra.Command {
	cmd := t.newCmd("task", nil)
	cmd.Short = "Task management commands"
	// todo: ask what this line does lol
	cmd.TraverseChildren = true
	cmd.Run = seeHelp
	cmd.AddCommand(
		t.taskLogCmd(),
		t.taskRunCmd(),
		t.taskCreateCmd(),
		t.taskDeleteCmd(),
		t.taskFindCmd(),
		t.taskUpdateCmd(),
		t.taskRerunFailedCmd(),
	)

	//todo add commands and return cmd
}

func (t *cmdTaskBuilder) newCmd(use string, runE func(*cobra.Command, []string) error) *cobra.Command {
	cmd := t.genericCLIOpts.newCmd(use, runE, true)
	t.globalFlags.registerFlags(t.viper, cmd)
	return cmd
}

//func cmdTask(f *globalFlags, opt genericCLIOpts) *cobra.Command {
//	runE := func(cmd *cobra.Command, args []string) error {
//		seeHelp(cmd, args)
//		return nil
//	}
//
//	cmd := opt.newCmd("task", runE, false)
//	cmd.Short = "Task management commands"
//
//	cmd.AddCommand(
//		taskLogCmd(f, opt),
//		taskRunCmd(f, opt),
//		taskCreateCmd(f, opt),
//		taskDeleteCmd(f, opt),
//		taskFindCmd(f, opt),
//		taskUpdateCmd(f, opt),
//		taskRerunFailedCmd(f, opt),
//	)
//
//	return cmd
//}

var taskPrintFlags struct {
	json        bool
	hideHeaders bool
}

var taskCreateFlags struct {
	org  organization
	file string
}

func (t *cmdTaskBuilder) taskCreateCmd() *cobra.Command {
	cmd := t.newCmd("create [script literal or -f /path/to/script.flux]", t.taskCreateF)
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.Short = "Create task"
	cmd.Long = `Create a task with a Flux script provided via the first argument or a file or stdin`

	f.registerFlags(opt.viper, cmd)
	cmd.Flags().StringVarP(&taskCreateFlags.file, "file", "f", "", "Path to Flux script file")
	taskCreateFlags.org.register(opt.viper, cmd, false)
	registerPrintOptions(opt.viper, cmd, &taskPrintFlags.hideHeaders, &taskPrintFlags.json)

	return cmd
}

func (t *cmdTaskBuilder) taskCreateF(cmd *cobra.Command, args []string) error {
	if err := taskCreateFlags.org.validOrgFlags(&flags); err != nil {
		return err
	}

	client, err := newHTTPClient()
	if err != nil {
		return err
	}

	s := &http.TaskService{
		Client: client,
	}

	flux, err := readFluxQuery(args, taskCreateFlags.file)
	if err != nil {
		return fmt.Errorf("error parsing flux script: %s", err)
	}

	tc := influxdb.TaskCreate{
		Flux:         flux,
		Organization: taskCreateFlags.org.name,
	}
	if taskCreateFlags.org.id != "" || taskCreateFlags.org.name != "" {
		svc, err := newOrganizationService()
		if err != nil {
			return nil
		}
		oid, err := taskCreateFlags.org.getID(svc)
		if err != nil {
			return fmt.Errorf("error parsing organization ID: %s", err)
		}
		tc.OrganizationID = oid
	}

	t, err := s.CreateTask(context.Background(), tc)
	if err != nil {
		return err
	}

	return printTasks(
		cmd.OutOrStdout(),
		taskPrintOpts{
			hideHeaders: taskPrintFlags.hideHeaders,
			json:        taskPrintFlags.json,
			task:        t,
		},
	)
}

var taskFindFlags struct {
	user    string
	id      string
	limit   int
	headers bool
	org     organization
}

func (t *cmdTaskBuilder) taskFindCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("list", taskFindF, true)
	cmd.Short = "List tasks"
	cmd.Aliases = []string{"find", "ls"}

	taskFindFlags.org.register(opt.viper, cmd, false)
	f.registerFlags(opt.viper, cmd)
	registerPrintOptions(opt.viper, cmd, &taskPrintFlags.hideHeaders, &taskPrintFlags.json)
	cmd.Flags().StringVarP(&taskFindFlags.id, "id", "i", "", "task ID")
	cmd.Flags().StringVarP(&taskFindFlags.user, "user-id", "n", "", "task owner ID")
	cmd.Flags().IntVarP(&taskFindFlags.limit, "limit", "", influxdb.TaskDefaultPageSize, "the number of tasks to find")
	cmd.Flags().BoolVar(&taskFindFlags.headers, "headers", true, "To print the table headers; defaults true")

	return cmd
}

func (t *cmdTaskBuilder) taskFindF(cmd *cobra.Command, args []string) error {
	if err := taskFindFlags.org.validOrgFlags(&flags); err != nil {
		return err
	}

	client, err := newHTTPClient()
	if err != nil {
		return err
	}

	s := &http.TaskService{
		Client: client,
	}

	filter := influxdb.TaskFilter{}
	if taskFindFlags.user != "" {
		id, err := influxdb.IDFromString(taskFindFlags.user)
		if err != nil {
			return err
		}
		filter.User = id
	}

	if taskFindFlags.org.name != "" {
		filter.Organization = taskFindFlags.org.name
	}
	if taskFindFlags.org.id != "" {
		id, err := influxdb.IDFromString(taskFindFlags.org.id)
		if err != nil {
			return err
		}
		filter.OrganizationID = id
	}

	if taskFindFlags.limit < 1 || taskFindFlags.limit > influxdb.TaskMaxPageSize {
		return fmt.Errorf("limit must be between 1 and %d", influxdb.TaskMaxPageSize)
	}
	filter.Limit = taskFindFlags.limit

	var tasks []*influxdb.Task

	if taskFindFlags.id != "" {
		id, err := influxdb.IDFromString(taskFindFlags.id)
		if err != nil {
			return err
		}

		task, err := s.FindTaskByID(context.Background(), *id)
		if err != nil {
			return err
		}

		tasks = append(tasks, task)
	} else {
		tasks, _, err = s.FindTasks(context.Background(), filter)
		if err != nil {
			return err
		}
	}

	return printTasks(
		cmd.OutOrStdout(),
		taskPrintOpts{
			hideHeaders: taskPrintFlags.hideHeaders,
			json:        taskPrintFlags.json,
			tasks:       tasks,
		},
	)
}

var taskRerunFailedFlags struct {
	taskId string
	before string
	after  string
	org    organization
}

func (t *cmdTaskBuilder) taskRerunFailedCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("rerun_failed", taskRerunFailedF, true)
	cmd.Short = "Find and Rerun failed runs/tasks"
	cmd.Aliases = []string{"rrf"}

	taskFindFlags.org.register(opt.viper, cmd, false)
	f.registerFlags(opt.viper, cmd)
	registerPrintOptions(opt.viper, cmd, &taskPrintFlags.hideHeaders, &taskPrintFlags.json)
	cmd.Flags().StringVarP(&taskRerunFailedFlags.taskId, "id", "i", "", "task ID")
	cmd.Flags().StringVarP(&taskRerunFailedFlags.before, "before", "bf", "", "before interval")
	cmd.Flags().StringVarP(&taskRerunFailedFlags.after, "after", "af", "", "after interval")

	return cmd
}

func (t *cmdTaskBuilder) taskRerunFailedF(command *cobra.Command, strings []string) error {
	if err := taskFindFlags.org.validOrgFlags(&flags); err != nil {
		return err
	}

	client, err := newHTTPClient()
	if err != nil {
		return err
	}

	s := &http.TaskService{
		Client: client,
	}

	taskIDPresent := taskRerunFailedFlags.taskId == ""
	// func do rerun(client, http.Tservice, taskID)

	/*
		If no TaskID is given, must use TaskFilter to get all Tasks and then search for failed runs then re run
		If TaskID given, use RunFilter to search for failed runs then re run
	*/
	taskFilter := influxdb.TaskFilter{}
	runFilter := influxdb.RunFilter{}
	if !taskIDPresent {
		if taskFindFlags.org.name != "" {
			taskFilter.Organization = taskFindFlags.org.name
		}
		if taskFindFlags.org.id != "" {
			orgID, err := influxdb.IDFromString(taskFindFlags.org.id)
			if err != nil {
				return err
			}
			taskFilter.OrganizationID = orgID
		}
	} else {
		id, err := influxdb.IDFromString(taskFindFlags.id)
		if err != nil {
			return err
		}
		runFilter.Task = *id
	}

	runFilter.BeforeTime = taskRerunFailedFlags.before
	runFilter.AfterTime = taskRerunFailedFlags.after

	var allRuns []*influxdb.Run
	if !taskIDPresent {
		allTasks, _, err := s.FindTasks(context.Background(), taskFilter)
		if err != nil {
			return err
		}

		for _, t := range allTasks {
			runFilter.Task = t.ID
			runsPerTask, _, err := s.FindRuns(context.Background(), runFilter)
			if err != nil {
				return err
			}
			allRuns = append(allRuns, runsPerTask...)
		}
	} else {
		allRuns, _, err = s.FindRuns(context.Background(), runFilter)
	}
	var failedRuns []*influxdb.Run
	for _, run := range allRuns {
		if run.Status == "failed" {
			failedRuns = append(failedRuns, run)
		}
	}
	for _, run := range failedRuns {
		newRun, err := s.RetryRun(context.Background(), run.TaskID, run.ID)
		if err != nil {
			return err
		}
		fmt.Printf("Retry for task %s's run %s queued as run %s.\n", run.TaskID, run.ID, newRun.ID)

	}
	return nil

}

var taskUpdateFlags struct {
	id     string
	status string
	file   string
}

func (t *cmdTaskBuilder) taskUpdateCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("update", taskUpdateF, true)
	cmd.Short = "Update task"
	cmd.Long = `Update task status or script. Provide a Flux script via the first argument or a file. Use '-' argument to read from stdin.`

	f.registerFlags(opt.viper, cmd)
	registerPrintOptions(opt.viper, cmd, &taskPrintFlags.hideHeaders, &taskPrintFlags.json)
	cmd.Flags().StringVarP(&taskUpdateFlags.id, "id", "i", "", "task ID (required)")
	cmd.Flags().StringVarP(&taskUpdateFlags.status, "status", "", "", "update task status")
	cmd.Flags().StringVarP(&taskUpdateFlags.file, "file", "f", "", "Path to Flux script file")
	cmd.MarkFlagRequired("id")

	return cmd
}

func (t *cmdTaskBuilder) taskUpdateF(cmd *cobra.Command, args []string) error {
	client, err := newHTTPClient()
	if err != nil {
		return err
	}

	s := &http.TaskService{
		Client: client,
	}

	var id influxdb.ID
	if err := id.DecodeFromString(taskUpdateFlags.id); err != nil {
		return err
	}

	var update influxdb.TaskUpdate
	if taskUpdateFlags.status != "" {
		update.Status = &taskUpdateFlags.status
	}

	// update flux script only if first arg or file is supplied
	if (len(args) > 0 && len(args[0]) > 0) || len(taskUpdateFlags.file) > 0 {
		flux, err := readFluxQuery(args, taskUpdateFlags.file)
		if err != nil {
			return fmt.Errorf("error parsing flux script: %s", err)
		}
		update.Flux = &flux
	}

	t, err := s.UpdateTask(context.Background(), id, update)
	if err != nil {
		return err
	}

	return printTasks(
		cmd.OutOrStdout(),
		taskPrintOpts{
			hideHeaders: taskPrintFlags.hideHeaders,
			json:        taskPrintFlags.json,
			task:        t,
		},
	)
}

var taskDeleteFlags struct {
	id string
}

func (t *cmdTaskBuilder) taskDeleteCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("delete", taskDeleteF, true)
	cmd.Short = "Delete task"

	f.registerFlags(opt.viper, cmd)
	registerPrintOptions(opt.viper, cmd, &taskPrintFlags.hideHeaders, &taskPrintFlags.json)
	cmd.Flags().StringVarP(&taskDeleteFlags.id, "id", "i", "", "task id (required)")
	cmd.MarkFlagRequired("id")

	return cmd
}

func (t *cmdTaskBuilder) taskDeleteF(cmd *cobra.Command, args []string) error {
	client, err := newHTTPClient()
	if err != nil {
		return err
	}

	s := &http.TaskService{
		Client: client,
	}

	var id influxdb.ID
	err = id.DecodeFromString(taskDeleteFlags.id)
	if err != nil {
		return err
	}

	ctx := context.TODO()
	t, err := s.FindTaskByID(ctx, id)
	if err != nil {
		return err
	}

	if err = s.DeleteTask(ctx, id); err != nil {
		return err
	}

	return printTasks(
		cmd.OutOrStdout(),
		taskPrintOpts{
			hideHeaders: taskPrintFlags.hideHeaders,
			json:        taskPrintFlags.json,
			task:        t,
		},
	)
}

type taskPrintOpts struct {
	hideHeaders bool
	json        bool
	task        *influxdb.Task
	tasks       []*influxdb.Task
}

func printTasks(w io.Writer, opts taskPrintOpts) error {
	if opts.json {
		var v interface{} = opts.tasks
		if opts.task != nil {
			v = opts.task
		}
		return writeJSON(w, v)
	}

	tabW := internal.NewTabWriter(os.Stdout)
	defer tabW.Flush()

	tabW.HideHeaders(opts.hideHeaders)

	tabW.WriteHeaders(
		"ID",
		"Name",
		"Organization ID",
		"Organization",
		"Status",
		"Every",
		"Cron",
	)

	if opts.task != nil {
		opts.tasks = append(opts.tasks, opts.task)
	}

	for _, t := range opts.tasks {
		tabW.Write(map[string]interface{}{
			"ID":              t.ID.String(),
			"Name":            t.Name,
			"Organization ID": t.OrganizationID.String(),
			"Organization":    t.Organization,
			"Status":          t.Status,
			"Every":           t.Every,
			"Cron":            t.Cron,
		})
	}

	return nil
}

func (t *cmdTaskBuilder) taskLogCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("log", nil, false)
	cmd.Run = seeHelp
	cmd.Short = "Log related commands"

	cmd.AddCommand(
		t.taskLogFindCmd(f, opt),
	)

	return cmd
}

var taskLogFindFlags struct {
	taskID string
	runID  string
}

func (t *cmdTaskBuilder) taskLogFindCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("list", taskLogFindF, true)
	cmd.Short = "List logs for task"
	cmd.Aliases = []string{"find", "ls"}

	f.registerFlags(opt.viper, cmd)
	registerPrintOptions(opt.viper, cmd, &taskPrintFlags.hideHeaders, &taskPrintFlags.json)
	cmd.Flags().StringVarP(&taskLogFindFlags.taskID, "task-id", "", "", "task id (required)")
	cmd.Flags().StringVarP(&taskLogFindFlags.runID, "run-id", "", "", "run id")
	cmd.MarkFlagRequired("task-id")

	return cmd
}

func (t *cmdTaskBuilder) taskLogFindF(cmd *cobra.Command, args []string) error {
	client, err := newHTTPClient()
	if err != nil {
		return err
	}

	s := &http.TaskService{
		Client: client,
	}

	var filter influxdb.LogFilter
	id, err := influxdb.IDFromString(taskLogFindFlags.taskID)
	if err != nil {
		return err
	}
	filter.Task = *id

	if taskLogFindFlags.runID != "" {
		id, err := influxdb.IDFromString(taskLogFindFlags.runID)
		if err != nil {
			return err
		}
		filter.Run = id
	}

	ctx := context.TODO()
	logs, _, err := s.FindLogs(ctx, filter)
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	if taskPrintFlags.json {
		return writeJSON(w, logs)
	}

	tabW := internal.NewTabWriter(w)
	defer tabW.Flush()

	tabW.HideHeaders(taskPrintFlags.hideHeaders)

	tabW.WriteHeaders("RunID", "Time", "Message")
	for _, log := range logs {
		tabW.Write(map[string]interface{}{
			"RunID":   log.RunID,
			"Time":    log.Time,
			"Message": log.Message,
		})
	}

	return nil
}

func (t *cmdTaskBuilder) taskRunCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("run", nil, false)
	cmd.Run = seeHelp
	cmd.Short = "List runs for a task"
	cmd.AddCommand(
		t.taskRunFindCmd(f, opt),
		t.taskRunRetryCmd(f, opt),
	)

	return cmd
}

var taskRunFindFlags struct {
	runID      string
	taskID     string
	afterTime  string
	beforeTime string
	limit      int
}

func (t *cmdTaskBuilder) taskRunFindCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("list", taskRunFindF, true)
	cmd.Short = "List runs for a task"
	cmd.Aliases = []string{"find", "ls"}

	f.registerFlags(opt.viper, cmd)
	registerPrintOptions(opt.viper, cmd, &taskPrintFlags.hideHeaders, &taskPrintFlags.json)
	cmd.Flags().StringVarP(&taskRunFindFlags.taskID, "task-id", "", "", "task id (required)")
	cmd.Flags().StringVarP(&taskRunFindFlags.runID, "run-id", "", "", "run id")
	cmd.Flags().StringVarP(&taskRunFindFlags.afterTime, "after", "", "", "after time for filtering")
	cmd.Flags().StringVarP(&taskRunFindFlags.beforeTime, "before", "", "", "before time for filtering")
	cmd.Flags().IntVarP(&taskRunFindFlags.limit, "limit", "", 100, "limit the results; default is 100")

	cmd.MarkFlagRequired("task-id")

	return cmd
}

func (t *cmdTaskBuilder) taskRunFindF(cmd *cobra.Command, args []string) error {
	client, err := newHTTPClient()
	if err != nil {
		return err
	}

	s := &http.TaskService{
		Client: client,
	}

	filter := influxdb.RunFilter{
		Limit:      taskRunFindFlags.limit,
		AfterTime:  taskRunFindFlags.afterTime,
		BeforeTime: taskRunFindFlags.beforeTime,
	}
	taskID, err := influxdb.IDFromString(taskRunFindFlags.taskID)
	if err != nil {
		return err
	}
	filter.Task = *taskID

	var runs []*influxdb.Run
	if taskRunFindFlags.runID != "" {
		id, err := influxdb.IDFromString(taskRunFindFlags.runID)
		if err != nil {
			return err
		}
		run, err := s.FindRunByID(context.Background(), filter.Task, *id)
		if err != nil {
			return err
		}
		runs = append(runs, run)
	} else {
		runs, _, err = s.FindRuns(context.Background(), filter)
		if err != nil {
			return err
		}
	}

	w := cmd.OutOrStdout()
	if taskPrintFlags.json {
		if runs == nil {
			// guarantee we never return a null value from CLI
			runs = make([]*influxdb.Run, 0)
		}
		return writeJSON(w, runs)
	}

	tabW := internal.NewTabWriter(w)
	defer tabW.Flush()

	tabW.HideHeaders(taskPrintFlags.hideHeaders)

	tabW.WriteHeaders(
		"ID",
		"TaskID",
		"Status",
		"ScheduledFor",
		"StartedAt",
		"FinishedAt",
		"RequestedAt",
	)

	for _, r := range runs {
		scheduledFor := r.ScheduledFor.Format(time.RFC3339)
		startedAt := r.StartedAt.Format(time.RFC3339Nano)
		finishedAt := r.FinishedAt.Format(time.RFC3339Nano)
		requestedAt := r.RequestedAt.Format(time.RFC3339Nano)

		tabW.Write(map[string]interface{}{
			"ID":           r.ID,
			"TaskID":       r.TaskID,
			"Status":       r.Status,
			"ScheduledFor": scheduledFor,
			"StartedAt":    startedAt,
			"FinishedAt":   finishedAt,
			"RequestedAt":  requestedAt,
		})
	}

	return nil
}

var runRetryFlags struct {
	taskID, runID string
}

func (t *cmdTaskBuilder) taskRunRetryCmd(f *globalFlags, opt genericCLIOpts) *cobra.Command {
	cmd := opt.newCmd("retry", t.runRetryF, true)
	cmd.Short = "retry a run"

	f.registerFlags(opt.viper, cmd)
	cmd.Flags().StringVarP(&runRetryFlags.taskID, "task-id", "i", "", "task id (required)")
	cmd.Flags().StringVarP(&runRetryFlags.runID, "run-id", "r", "", "run id (required)")
	cmd.MarkFlagRequired("task-id")
	cmd.MarkFlagRequired("run-id")

	return cmd
}

func (t *cmdTaskBuilder) runRetryF(cmd *cobra.Command, args []string) error {
	client, err := newHTTPClient()
	if err != nil {
		return err
	}

	s := &http.TaskService{
		Client: client,
	}

	var taskID, runID influxdb.ID
	if err := taskID.DecodeFromString(runRetryFlags.taskID); err != nil {
		return err
	}
	if err := runID.DecodeFromString(runRetryFlags.runID); err != nil {
		return err
	}

	ctx := context.TODO()
	newRun, err := s.RetryRun(ctx, taskID, runID)
	if err != nil {
		return err
	}

	fmt.Printf("Retry for task %s's run %s queued as run %s.\n", taskID, runID, newRun.ID)

	return nil
}
