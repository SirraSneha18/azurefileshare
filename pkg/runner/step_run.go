package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/nektos/act/pkg/common"
	"github.com/nektos/act/pkg/container"
	"github.com/nektos/act/pkg/model"
	log "github.com/sirupsen/logrus"
)

func (sc *StepContext) setupShellCommandExecutor() common.Executor {
	rc := sc.RunContext
	return func(ctx context.Context) error {
		scriptName, script, err := sc.setupShellCommand()
		if err != nil {
			return err
		}

		return rc.JobContainer.Copy(ActPath, &container.FileEntry{
			Name: scriptName,
			Mode: 0755,
			Body: script,
		})(ctx)
	}
}

func getScriptName(rc *RunContext, step *model.Step) string {
	scriptName := step.ID
	for rcs := rc; rcs.Parent != nil; rcs = rcs.Parent {
		scriptName = fmt.Sprintf("%s-composite-%s", rcs.Parent.CurrentStep, scriptName)
	}
	return fmt.Sprintf("workflow/%s", scriptName)
}

// TODO: Currently we just ignore top level keys, BUT we should return proper error on them
// BUTx2 I leave this for when we rewrite act to use actionlint for workflow validation
// so we return proper errors before any execution or spawning containers
// it will error anyway with:
// OCI runtime exec failed: exec failed: container_linux.go:380: starting container process caused: exec: "${{": executable file not found in $PATH: unknown
func (sc *StepContext) setupShellCommand() (name, script string, err error) {
	sc.setupShell()
	sc.setupWorkingDirectory()

	step := sc.Step

	script = sc.RunContext.ExprEval.Interpolate(step.Run)

	scCmd := step.ShellCommand()

	name = getScriptName(sc.RunContext, step)

	// Reference: https://github.com/actions/runner/blob/8109c962f09d9acc473d92c595ff43afceddb347/src/Runner.Worker/Handlers/ScriptHandlerHelpers.cs#L47-L64
	// Reference: https://github.com/actions/runner/blob/8109c962f09d9acc473d92c595ff43afceddb347/src/Runner.Worker/Handlers/ScriptHandlerHelpers.cs#L19-L27
	runPrepend := ""
	runAppend := ""
	switch step.Shell {
	case "bash", "sh":
		name += ".sh"
	case "pwsh", "powershell":
		name += ".ps1"
		runPrepend = "$ErrorActionPreference = 'stop'"
		runAppend = "if ((Test-Path -LiteralPath variable:/LASTEXITCODE)) { exit $LASTEXITCODE }"
	case "cmd":
		name += ".cmd"
		runPrepend = "@echo off"
	case "python":
		name += ".py"
	}

	script = fmt.Sprintf("%s\n%s\n%s", runPrepend, script, runAppend)

	log.Debugf("Wrote command \n%s\n to '%s'", script, name)

	scriptPath := fmt.Sprintf("%s/%s", ActPath, name)
	sc.Cmd, err = shellquote.Split(strings.Replace(scCmd, `{0}`, scriptPath, 1))

	return name, script, err
}

func (sc *StepContext) setupShell() {
	rc := sc.RunContext
	step := sc.Step

	if step.Shell == "" {
		step.Shell = rc.Run.Job().Defaults.Run.Shell
	}

	step.Shell = rc.ExprEval.Interpolate(step.Shell)

	if step.Shell == "" {
		step.Shell = rc.Run.Workflow.Defaults.Run.Shell
	}

	// current GitHub Runner behaviour is that default is `sh`,
	// but if it's not container it validates with `which` command
	// if `bash` is available, and provides `bash` if it is
	// for now I'm going to leave below logic, will address it in different PR
	// https://github.com/actions/runner/blob/9a829995e02d2db64efb939dc2f283002595d4d9/src/Runner.Worker/Handlers/ScriptHandler.cs#L87-L91
	if rc.Run.Job().Container() != nil {
		if rc.Run.Job().Container().Image != "" && step.Shell == "" {
			step.Shell = "sh"
		}
	}
}

func (sc *StepContext) setupWorkingDirectory() {
	rc := sc.RunContext
	step := sc.Step

	if step.WorkingDirectory == "" {
		step.WorkingDirectory = rc.Run.Job().Defaults.Run.WorkingDirectory
	}

	// jobs can receive context values, so we interpolate
	step.WorkingDirectory = rc.ExprEval.Interpolate(step.WorkingDirectory)

	// but top level keys in workflow file like `defaults` or `env` can't
	if step.WorkingDirectory == "" {
		step.WorkingDirectory = rc.Run.Workflow.Defaults.Run.WorkingDirectory
	}
}
