package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

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

	// failedTestResults is a list of failed tests whose logs will be uploaded as artifacts.
	failedTestResults []*failedTest
	// failedActions is a list of actions that did not succeed, whose output will be used to annotate
	// the build for more clarity.
	failedActions []*failedAction

	// analyticsResults collects test results that will be sent to Buildkite Analytics.
	analyticsResults []TestResult
}

type pluginProperties struct {
	// BuildkiteAgentPath stores the path of the buildkite-agent binary,
	// see to https://buildkite.com/docs/agent/v3/cli-artifact.
	// Defaults to "" (plugin will assume that buildkite-agent is in the $PATH).
	BuildkiteAgentPath string `yaml:"buildkite_agent_path"`
	// Pretend, if true, makes the plugin output the builkdite-agent commands instead
	// of executing them, useful for local development.
	Pretend bool `yaml:"pretend"`
}

// failedAction is small struct to hold the results from a failed action.
type failedAction struct {
	label     string
	stderrURI string
	stdoutURI string
}

type failedTest struct {
	result *buildeventstream.TestResult
	label  string
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
		var result = "passed"
		if testResult.Status == buildeventstream.TestStatus_FAILED {
			p.failedTestResults = append(p.failedTestResults, &failedTest{result: testResult, label: label})
			result = "failed"
		}

		// If it's a cache miss, we're really executing the test, so we record it.
		if !testResult.GetCachedLocally() && !testResult.GetExecutionInfo().GetCachedRemotely() {
			tr := NewTestResult(
				label,
				testResult.TestAttemptStartMillisEpoch,
				float64(testResult.TestAttemptDurationMillis)/1000,
				result,
			)
			p.analyticsResults = append(p.analyticsResults, tr)
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
	// if err := PostResults(context.Background(), p.analyticsResults); err != nil {
	// 	return err
	// }
	if err := SaveTestResults(p.analyticsResults); err != nil {
		return err
	}
	return p.hook(interactive, pr)
}

func (p *BuildkitePlugin) PostBuildHook(interactive bool, pr ioutils.PromptRunner) error {
	return p.hook(interactive, pr)
}

func (p *BuildkitePlugin) PostRunHook(interactive bool, pr ioutils.PromptRunner) error {
	return p.hook(interactive, pr)
}

func (p *BuildkitePlugin) hook(_ bool, pr ioutils.PromptRunner) error {
	return nil
	if !p.inBuildkite() {
		return nil
	}
	ll, _ := os.Create("__log.txt")
	defer ll.Close()

	ctx := context.Background()
	for _, result := range p.failedTestResults {
		for _, f := range result.result.GetTestActionOutput() {
			if f.GetName() == "test.log" {
				path, err := p.outputClient.GetFilePath(ctx, f.GetUri(), f.GetName())
				if err != nil {
					return err
				}
				if err := p.agent.UploadArtifacts(ctx, path); err != nil {
					return err
				}
			}
		}
		m := renderFailedTestMarkdown(ctx, result)
		if err := p.agent.Annotate(ctx, "error", "failed_test", []byte(m)); err != nil {
			return err
		}
	}

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

func renderFailedTestMarkdown(ctx context.Context, ft *failedTest) string {
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
