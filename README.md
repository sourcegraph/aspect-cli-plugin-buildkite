# Buildkite plugin for aspect-cli

This repo provides a [Buildkite](https://buildkite.com) plugin for the [aspect cli](https://aspect.build/cli).

## Status 

This is very much a WIP effort. Do not use in your own pipelines (yet).

## Contribute

The best way I've found to iterate on this is to: 

- Clone that repo locally and put it next to another repo that will use it as a plugin
- Use the following snippet in `.aspect/cli/config.yaml`: 

```
plugins:
  - name: buildkite
    from: ../aspect-cli-plugin-buildkite/bazel-bin/plugin
    log_level: debug
    properties:
      pretend: true
```

- Understanding [BEP](https://bazel.build/remote/bep) is not easy at first. Build whatever target you want to enhance with the flag `--build_event_json_file=bep.json` and inspect what's in there to get a better grasp at what events the code should react. 

- A mocked version of `buildkite-agent` cli is provided under `//cmd/mockagent`. It does nothing else that dumping its args and stdin in `/tmp/_log_mock_agent.txt`. Set the property `buildkite_agent_path` to its compiled path to tell the plugin to use that binary instead of `buildkite-agent`.

- At some point, it's mandatory to test things against a real Buildkite build ran by an agent, which requires the plugin to be available. The repository is configured to build a release once a tag is pushed (`vX.Y.Z-pre`) so just push a tag and turn the automatically created draft release into a pre-release, which you can then use in any pipeline to test the result.

## Demo

> TODO: Consider showing off your new plugin with a little animated demo of your terminal! We highly recommend [asciinema](https://asciinema.org).
