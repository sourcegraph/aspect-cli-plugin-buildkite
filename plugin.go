package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/sourcegraph/aspect-cli-plugin-buildkite/bazel/outputfile"
	"gopkg.in/yaml.v2"

	"aspect.build/cli/bazel/buildeventstream"
	"aspect.build/cli/pkg/ioutils"
	"aspect.build/cli/pkg/plugin/sdk/v1alpha3/config"
	aspectplugin "aspect.build/cli/pkg/plugin/sdk/v1alpha3/plugin"
)

// main starts up the plugin as a child process of the CLI and connects the gRPC communication.
func main() {
	goplugin.Serve(config.NewConfigFor(&BuildkitePlugin{}))
}

// BuildkitePlugin declares the fields on an instance of the plugin.
type BuildkitePlugin struct {
	// Base gives default implementations of the plugin methods, so implementing them below is optional.
	// See the definition of aspectplugin.Base for more methods that can be implemented by the plugin.
	aspectplugin.Base

	// agent is an interface wrapping access to the buildkite-agent binary.
	// see https://buildkite.com/docs/agent/v3/cli-reference.
	agent BuildkiteAgent
	// outputClient handles reading from URI returned by the various event while abstracting away the different
	// schemes so they can all be treated as local files.
	outputClient *outputfile.Client

	// testResultInfos is a list of failed tests whose logs will be uploaded as artifacts.
	testResultInfos []*testResultInfo

	// failedActions is a list of actions that did not succeed, whose output will be used to annotate
	// the build for more clarity.
	failedActions []*failedAction

	// buildkiteAnalyticsTokenName is the name of the env var we should be reading
	// the token from or defaults to read it from "$BUILDKITE_ANALYTICS_TOKEN".
	buildkiteAnalyticsToken string

	// isPreamblePosted tells us if we have posted or not the preamble, as we don't have
	// a final or first hook to run things before or after the completion of the entire build.
	isPreamblePosted bool
}

type pluginProperties struct {
	// BuildkiteAgentPath stores the path of the buildkite-agent binary,
	// see to https://buildkite.com/docs/agent/v3/cli-artifact.
	// Defaults to "" (plugin will assume that buildkite-agent is in the $PATH).
	BuildkiteAgentPath string `yaml:"buildkite_agent_path"`

	// Pretend, if true, makes the plugin output the builkdite-agent commands instead
	// of executing them, useful for local development.
	Pretend bool `yaml:"pretend"`

	// BuildkiteAnalyticsTokenName is the name of the env var we should be reading
	// the token from. The default env var name is "BUILDKITE_ANALYTICS_TOKEN".
	BuildkiteAnalyticsTokenName string `yaml:"buildkite_analytics_env_name"`
}

// failedAction is small struct to hold the results from a failed action.
type failedAction struct {
	label     string
	stderrURI string
	stdoutURI string
}

type testResultInfo struct {
	result *buildeventstream.TestResult
	label  string
	cached bool
}

func (tr *testResultInfo) Failed() bool {
	status := tr.result.GetStatus()
	return status == buildeventstream.TestStatus_FAILED ||
		status == buildeventstream.TestStatus_REMOTE_FAILURE ||
		status == buildeventstream.TestStatus_TIMEOUT
}

func (tr *testResultInfo) FailureReason() string {
	switch tr.result.GetStatus() {
	case buildeventstream.TestStatus_NO_STATUS:
		return "no_status"
	case buildeventstream.TestStatus_FAILED:
		return "failed"
	case buildeventstream.TestStatus_FLAKY:
		return "flaky"
	case buildeventstream.TestStatus_TIMEOUT:
		return "timeout"
	case buildeventstream.TestStatus_REMOTE_FAILURE:
		return "remote_failure"
	case buildeventstream.TestStatus_FAILED_TO_BUILD:
		return "failed_to_build"
	case buildeventstream.TestStatus_TOOL_HALTED_BEFORE_TESTING:
		return "tool_halted_before_testing"
	default:
		return ""
	}
}

func (tr *testResultInfo) AnalyticsPayload(testLogPath string) (*AnalyticsTestPayload, error) {
	result := "passed"
	failureExpanded := []map[string][]string{}
	var failureReason *string

	if tr.Failed() {
		// record it for our payload
		result = "failed"

		// extract the logs
		f, err := os.Open(testLogPath)
		defer f.Close()
		if err != nil {
			return nil, err
		}

		var lines []string
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}

		if err := scanner.Err(); err != nil {
			return nil, err
		}

		// Store the logs in the payload.
		failureExpanded = append(failureExpanded, map[string][]string{"expanded": lines})

		// Record the failure reason.
		reason := tr.FailureReason()
		failureReason = &reason
	}

	return &AnalyticsTestPayload{
		ID:              uuid.NewString(),
		Name:            tr.label,
		Result:          result,
		FailureReason:   failureReason,
		FailureExpanded: failureExpanded,
		History: History{
			StartAt:       tr.result.TestAttemptStartMillisEpoch,
			EndAt:         tr.result.TestAttemptStartMillisEpoch + tr.result.TestAttemptDurationMillis,
			DurationInSec: float64(tr.result.TestAttemptDurationMillis) / 1000,
		},
	}, nil
}

// inBuildkite returns true if we detect that we're running inside a Buildkite agent.
func (p *BuildkitePlugin) inBuildkite() bool {
	return os.Getenv("BUILDKITE") == "true"
}

func (p *BuildkitePlugin) Setup(config *aspectplugin.SetupConfig) error {
	// Parse plugin configuration properties
	var props pluginProperties
	if err := yaml.Unmarshal(config.Properties, &props); err != nil {
		return fmt.Errorf("failed to setup: failed to parse properties: %w", err)
	}

	// Read the BuildkiteAnalytics token from the env.
	tokvar := props.BuildkiteAnalyticsTokenName
	if tokvar == "" {
		tokvar = "BUILDKITE_ANALYTICS_TOKEN"
	}
	p.buildkiteAnalyticsToken = os.Getenv(tokvar)

	// Prepare buildkiteagent that we use to interact with Buildkite
	if !props.Pretend {
		p.agent = NewBuildkiteAgent(props.BuildkiteAgentPath)
	} else {
		p.agent = NewMockBuildkiteAgent(props.BuildkiteAgentPath)
	}

	// Create a client to read URIs, as they can be files or bytestream if a remote-cache is enabled.
	p.outputClient = outputfile.NewClient()

	return nil
}

// BEPEventCallback subscribes to all Build Events, and lets our logic react to ones we care about.
func (p *BuildkitePlugin) BEPEventCallback(event *buildeventstream.BuildEvent) error {
	if !p.inBuildkite() {
		return nil
	}

	switch event.Payload.(type) {
	case *buildeventstream.BuildEvent_TestResult:
		testResult := event.GetTestResult()
		label := event.Id.GetTestResult().GetLabel()

		tr := testResultInfo{
			result: testResult,
			label:  label,
			cached: testResult.GetCachedLocally() || testResult.GetExecutionInfo().GetCachedRemotely(),
		}

		if !tr.cached {
			p.testResultInfos = append(p.testResultInfos, &tr)
		}

	case *buildeventstream.BuildEvent_Action:
		action := event.GetAction()
		if !action.GetSuccess() {
			p.failedActions = append(p.failedActions, &failedAction{
				label:     event.GetId().GetActionCompleted().GetLabel(),
				stderrURI: action.GetStderr().GetUri(),
				stdoutURI: action.GetStdout().GetUri(),
			})
		}
	}
	return nil
}

func (p *BuildkitePlugin) PostTestHook(interactive bool, pr ioutils.PromptRunner) error {
	return p.hook(interactive, pr)
}

func (p *BuildkitePlugin) PostBuildHook(interactive bool, pr ioutils.PromptRunner) error {
	return p.hook(interactive, pr)
}

func (p *BuildkitePlugin) PostRunHook(interactive bool, pr ioutils.PromptRunner) error {
	return p.hook(interactive, pr)
}

func (p *BuildkitePlugin) hook(_ bool, pr ioutils.PromptRunner) error {
	if !p.inBuildkite() {
		return nil
	}

	ctx := context.Background()
	if err := p.annotateFailedTests(ctx); err != nil {
		return err
	}
	if err := p.annotateFailedActions(ctx); err != nil {
		return err
	}
	if err := p.postTestAnalytics(ctx); err != nil {
		return err
	}
	return nil
}

// testPreamble is the text posted before anything else in the error annotation at the top of the build
// if there is a failed test detected the build. The final two line breaks are important, because they
// allow the formatting to be readable when we're posting the list of failed test targets.
var testPreamble = `:bulb: You can run the following failed test targets with 'bazel test [target]' locally on your
machine to reproduce the issues and iterate faster than having to wait for the CI again.

If a particular test target is too slow to build, you can also use 'sg ci bazel test [target]' to have the CI run that 
only particular target.


`

func (p *BuildkitePlugin) annotateFailedTests(ctx context.Context) error {
	if len(p.testResultInfos) > 0 && !p.isPreamblePosted {
		p.isPreamblePosted = true
		if err := p.agent.Annotate(ctx, "error", "failed_test", []byte(testPreamble)); err != nil {
			return err
		}
	}

	for _, result := range p.testResultInfos {
		var testLogPath string

		for _, f := range result.result.GetTestActionOutput() {
			if f.GetName() == "test.log" {
				path, err := p.outputClient.GetFilePath(ctx, f.GetUri(), f.GetName())
				if err != nil {
					return err
				}
				testLogPath = path
			}
		}

		// If the test failed, annotate and upload the artifact.
		if result.Failed() {
			m := renderFailedTestMarkdown(ctx, result)
			if err := p.agent.Annotate(ctx, "error", "failed_test", []byte(m)); err != nil {
				return err
			}
			if testLogPath != "" {
				if err := p.agent.UploadArtifacts(ctx, testLogPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (p *BuildkitePlugin) annotateFailedActions(ctx context.Context) error {
	for _, action := range p.failedActions {
		m, err := renderFailedActionMarkdown(ctx, p.outputClient, action)
		if err != nil {
			return err
		}
		if err := p.agent.Annotate(ctx, "error", "failed_actions", []byte(m)); err != nil {
			return err
		}
	}
	return nil
}

func (p *BuildkitePlugin) postTestAnalytics(ctx context.Context) error {
	payloads := []*AnalyticsTestPayload{}
	for _, result := range p.testResultInfos {
		var testLogPath string

		for _, f := range result.result.GetTestActionOutput() {
			if f.GetName() == "test.log" {
				path, err := p.outputClient.GetFilePath(ctx, f.GetUri(), f.GetName())
				if err != nil {
					return err
				}
				testLogPath = path
			}
		}

		payload, err := result.AnalyticsPayload(testLogPath)
		if err != nil {
			return err
		}
		payloads = append(payloads, payload)
	}

	if p.buildkiteAnalyticsToken != "" {
		// return SaveTestResults(payloads)
		return PostResults(ctx, p.buildkiteAnalyticsToken, payloads)
	}
	return nil
}

func renderFailedTestMarkdown(ctx context.Context, ft *testResultInfo) string {
	return fmt.Sprintf("- **Failed test** `%s`\n", ft.label)
}

func renderFailedActionMarkdown(ctx context.Context, client *outputfile.Client, fa *failedAction) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Action failed: `%s`**\n", fa.label))
	if fa.stdoutURI != "" {
		out, err := client.Open(ctx, fa.stdoutURI)
		if err != nil {
			return "", err
		}
		defer out.Close()
		sb.WriteString("_stdout_:\n")
		sb.WriteString("```term")
		if _, err := io.Copy(&sb, out); err != nil {
			return "", err
		}
		sb.WriteString("\n```\n")
	}
	if fa.stderrURI != "" {
		out, err := client.Open(ctx, fa.stderrURI)
		if err != nil {
			return "", err
		}
		defer out.Close()
		sb.WriteString("_stderr_:\n")
		sb.WriteString("```term\n")
		if _, err := io.Copy(&sb, out); err != nil {
			return "", err
		}
		sb.WriteString("\n```\n")
	}
	return sb.String(), nil
}
