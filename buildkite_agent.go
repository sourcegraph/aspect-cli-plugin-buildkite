package main

import (
	"context"
	"fmt"
	"os/exec"
)

type BuildkiteAgent interface {
	UploadArtifacts(ctx context.Context, glob string) error
	Annotate(ctx context.Context, style string, annotationContext string, markdown []byte) error
}

type buildkiteAgent struct {
	path string
}

func NewBuildkiteAgent(path string) BuildkiteAgent {
	p := "buildkite-agent"
	if path != "" {
		p = path
	}
	return &buildkiteAgent{path: p}
}

func (a *buildkiteAgent) UploadArtifacts(ctx context.Context, glob string) error {
	return exec.CommandContext(ctx, a.path, "artifact", "upload", glob).Run()
}

func (a *buildkiteAgent) Annotate(ctx context.Context, style string, aCtx string, m []byte) error {
	cmd := exec.CommandContext(ctx, a.path, fmt.Sprintf("annotate --style %s --context %s", style, aCtx))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil
	}
	go func() {
		defer stdin.Close()
		_, _ = stdin.Write(m)
	}()
	return cmd.Run()
}

type mockBuildkiteAgent struct {
	path string
}

func NewMockBuildkiteAgent(path string) BuildkiteAgent {
	p := "buildkite-agent"
	if path != "" {
		p = path
	}
	return &mockBuildkiteAgent{path: p}
}

func (a *mockBuildkiteAgent) UploadArtifacts(ctx context.Context, glob string) error {
	fmt.Printf("%s artifact upload %q\n", a.path, glob)
	return nil
}

func (a *mockBuildkiteAgent) Annotate(ctx context.Context, style string, aCtx string, m []byte) error {
	fmt.Printf("%s annotate --style %s --context %s <<EOF", a.path, style, aCtx)
	fmt.Printf("%sEOF", string(m))
	return nil
}
