# AGENT.md - Development Guide

## Commands
- **Build**: `bazel build :dev`
- **Build plugin locally**: `bazel build //:aspect-cli-plugin-sg`
- **Update deps**: `bazel run //:update_go_deps`
- **Run gazelle**: `bazel run //:gazelle`

## Development Workflow
- After building, user must update `.aspect/cli/config.yaml` to point to `bazel-bin/plugin`
- Testing requires running in the Sourcegraph monorepo with the plugin configured (see README)
- No automated tests exist - testing is done via integration with aspect-cli

## Code Style (Go)
- **Imports**: stdlib first, blank line, external deps, blank line, local packages
- **Naming**: PascalCase types, camelCase functions/vars, descriptive interface names
- **Error handling**: wrap with context using `fmt.Errorf("context: %w", err)`, early returns
- **Functions**: context first param, pointer receivers, explicit returns preferred
- **Formatting**: follow standard Go formatting, gofmt compliance

## Project Structure
- Main plugin code in root (plugin.go, buildkite_agent.go, etc.)
- Bazel build system with BUILD.bazel files
- No tests currently exist - should be added as `*_test.go` files
- Uses aspect-cli plugin framework with gRPC communication
- Mock agent available at `//cmd/mockagent` for testing
