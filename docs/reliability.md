# System and reliability brief

agent-hackathon-go26 is a single Go binary that connects one model to many
external apps through two builtin tools: `shell` and `load_skill`. This
document covers what the system guarantees, what it does when things go
wrong, and how we check that.

## Design in one paragraph

The agent loop streams a model round, executes its tool calls, feeds results
back, and repeats until the model replies without calling tools. The model
sees exactly two builtin tools, so every app integration is a skill, a
`SKILL.md` that teaches the agent how to drive one CLI. There is no per-app
client code to break, no SDK versions to drift, and adding an app cannot
touch the agent core. The shell is the adapter for everything, so the
reliability work sits on the three layers that actually fail in practice:
the provider, the tool boundary, and the conversation state.

## What fails, and what happens

| Failure | Behavior | Why it is safe |
|---|---|---|
| Gateway 429 / 5xx / 408 / 409 | retried up to 6 times, exponential backoff starting at 2s with 25 percent jitter, capped at 30s, `Retry-After` honored when present | transient provider load never kills a multi-step run, and a retry cannot fix a 400 so those fail fast |
| Connection drops mid-request (reset, refused, broken pipe, EOF, DNS) | classified by pattern and retried on the same path as status codes | one retry budget covers both, retries keep the full conversation |
| Stream goes silent (gateway accepts, never sends) | idle watchdog cancels the stream after 5 minutes without a byte, the timeout is then retried like any other transient failure | without it a hung proxy would block a turn forever with no recovery |
| Tool call exceeds its deadline | context deadline cancels the process group (`SIGKILL` to the whole tree), the result reports the timeout instead of erroring the run | one stuck command cannot wedge the loop, and background children die with it |
| Command produces huge output | capped at 512 KiB at the pipe, results clamped per tool at the registry | one noisy command cannot flood the model's context |
| Tool returns a non-zero exit | exit code reported inside the result, the model sees it and can adapt | a failing CLI is information, not an exception |
| Model loops on tools | hard budget of 100 tool rounds per turn | runaway sessions end deterministically |
| Interrupt (ctrl-c) | signal context unwinds the run cleanly | transcripts persist per event, nothing partial is left behind |

## Transcripts

Every run appends `runs/<timestamp>.jsonl`, one JSON object per event:
`user_message`, `tool_call` (name plus raw arguments), `tool_result` (full
result, error, if any), `retry` (attempt, delay, provider error), `error`
(ends a failed run), and `done` (token usage). Writes are unbuffered, so a
crash keeps everything up to the last event, and any run's tool activity is
reviewable after the fact, line by line.

## Evaluation

Two layers, both checked before any demo:

1. **Static checks** offline: `go vet ./...` and `gofmt` clean, plus a
   `go build` from a zero-dependency module so the whole thing compiles
   anywhere in seconds.

2. **Live scenarios** (`evals/eval.sh`) run the real binary against the real
   model and real CLIs, then assert on the JSONL transcript and the final
   output: a direct answer must not call tools, a computation must go through
   `shell`, and each app task must load its skill first. The harness exits
   non-zero on any missed assertion, so it can gate a demo.

## Honest limits

- There is no approval gate: the agent runs with the operator's permissions
  in the operator's chosen working directory. This is a single-operator
  agent, the trust boundary is the shell prompt itself.
- The context window is finite: long sessions accumulate history in memory
  with no compaction yet. Sessions are expected to be task-length.
- Retries duplicate any on-screen partial output from the failed attempt.
  The transcript records only the successful attempt's text.
- `Retry-After` from the gateway is honored up to a 2 minute ceiling, after
  that the attempt budget ends the run.
