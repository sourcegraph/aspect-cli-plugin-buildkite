package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"

	"github.com/google/uuid"
)

type History struct {
	StartAt       int64   `json:"start_at"`
	EndAt         int64   `json:"end_at"`
	DurationInSec float64 `json:"duration"`
}

type AnalyticsTestPayload struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	History         History             `json:"history"`
	Result          string              `json:"result"`
	FailureReson    string              `json:"failure_reason"`
	FailureExpanded map[string][]string `json:"failure_expanded"`
}

func NewTestResult(target string, start int64, end int64, duration float64, result string) AnalyticsTestPayload {
	return AnalyticsTestPayload{
		ID:     uuid.NewString(),
		Name:   target,
		Result: result,
		History: History{
			StartAt:       start,
			EndAt:         end,
			DurationInSec: duration,
		},
	}
}

func PostResults(ctx context.Context, token string, results []*AnalyticsTestPayload) error {
	if len(results) == 0 || token == "" {
		return nil
	}

	var buf bytes.Buffer
	formWriter := multipart.NewWriter(&buf)

	b, err := json.Marshal(results)
	if err != nil {
		return err
	}

	formWriter.WriteField("format", "json")
	formWriter.WriteField("run_env[CI]", "buildkite")
	formWriter.WriteField("run_env[key]", os.Getenv("BUILDKITE_BUILD_ID"))
	formWriter.WriteField("run_env[url]", os.Getenv("BUILDKITE_BUILD_URL"))
	formWriter.WriteField("run_env[branch]", os.Getenv("BUILDKITE_BRANCH"))
	formWriter.WriteField("run_env[commit_sha]", os.Getenv("BUILDKITE_COMMIT"))
	formWriter.WriteField("run_env[number]", os.Getenv("BUILDKITE_BUILD_NUMBER"))
	formWriter.WriteField("run_env[job_id]", os.Getenv("BUILDKITE_JOB_ID"))
	formWriter.WriteField("run_env[message]", os.Getenv("BUILDKITE_MESSAGE"))

	part, err := formWriter.CreateFormField("data")
	if err != nil {
		return err
	}
	if _, err := part.Write(b); err != nil {
		return err
	}
	if err := formWriter.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://analytics-api.buildkite.com/v1/uploads", &buf)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Token token=\"%s\"", token))
	req.Header.Set("Content-Type", formWriter.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 {
		return nil
	} else {
		return errors.New(fmt.Sprintf("status code = %d", resp.StatusCode))
	}
}

func SaveTestResults(res []*AnalyticsTestPayload) error {
	f, err := os.Create("testresults.json")
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(res)
}
