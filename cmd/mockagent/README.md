# Mock buildkite-agent 

Simple local replacement for `buildkite-agent` binary that is available within agents when running jobs. 
It basically prints out in `/tmp/_log_mock_agent.txt` the args and inputs it was called with.
