# agent-hackathon-go26

A slim multi-app AI agent in one Go binary, under 1,700 lines, no
third-party dependencies. Two builtin tools, and app integrations that are
just markdown skills wrapping CLIs you already trust.

Built for the Multi-App AI Agent Hackathon (Lemma AI x Comma Capital,
Sep 13, 2026).

## Demos

![Cross-app security triage, 43 seconds](demo/demo.gif)

Demo one, 43 seconds, one prompt, three apps. The agent reads a security
disclosure in Basecamp, finds the matching issue and pull request on GitHub,
and sends a summary email over HEY. A rate-limit error hits halfway through,
the agent retries and carries on. [demo/demo.mp4](demo/demo.mp4) is the same
recording as a video file. To run it for real yourself, authenticate the
three CLIs below and replay the prompt, or let `evals/eval.sh` check the
behavior against the live model. Expect it to differ from the recording, the
exact words and tool choices come from your model, gateway, and effort
settings.

I sit on the [Omarchy security team](https://omarchy.org/teams#security).
Omarchy is managed in Basecamp, discussed over HEY, and built on GitHub, so a
disclosure triage like this one is regular work, the team does it daily with
agents, and this repo is the tool I built for it.

![Weather-triggered email, 42 seconds](demo/demo2.gif)

Demo two, 42 seconds, two turns and a condition. Current weather in New York
comes back over curl from wttr.in, no skill needed, then the ask: check
tomorrow and if it is going to rain, tell the team. The forecast says 78
percent, so the agent loads the hey skill and sends the heads-up.
[demo/demo2.mp4](demo/demo2.mp4) is the same as a video file. This one shows
the other half of the point, the agent acts on a condition instead of just
fetching facts.

## The idea

Most agent integrations are code: one client per app, one auth flow per app,
all baked in. This agent has none of that. It ships exactly two builtin tools:

- `shell` — run a command
- `load_skill` — read instructions for a skill on demand

Everything else is a skill: a folder with a `SKILL.md` describing an app and
the CLI that drives it. Three apps, three markdown files:

| Skill | CLI | What it is | What the agent can do |
|---|---|---|---|
| basecamp | [basecamp](https://github.com/basecamp/basecamp-cli) | official Basecamp CLI, ships its own agent skills | create todos, move cards, post messages, search |
| hey | [hey](https://github.com/basecamp/hey-cli) | official HEY CLI, ships its own agent skills | read mail, reply, compose, screen senders |
| github | [gh](https://github.com/cli/cli) | GitHub's official command line tool | issues, pull requests, Actions runs |

Adding a fourth app is a markdown file, not a pull request into an SDK layer.
The three apps here are only the demo. `shell` can already run anything
installed on the machine, so any CLI, API or tool is one skill file away, from
local repos to internal dashboards. The agent discovers skills in
`~/.agents/skills` (global) and `./.agents/skills` (project wins), so the same
agent is useful in every project on the machine.

The three skills in this repo are trimmed to the commands the demo uses. For
real workloads install the full ones: the `basecamp` and `hey` CLIs each ship
their official skill (`basecamp skill install`, `hey skill install`) into the
global `~/.agents/skills`, and this agent picks them up unchanged.

## Quickstart

Grab [Go 1.27 or newer](https://go.dev/dl/) first, it is the only thing the
build needs.

```sh
cp .env.example .env   # then fill in AGENT_BASE_URL, AGENT_API_KEY, AGENT_MODEL
go build -o agent-hackathon-go26 .
./agent-hackathon-go26                      # interactive session
./agent-hackathon-go26 "list my basecamp projects"
```

Apps are reached through their official CLIs, no SDKs involved. The demo
uses `basecamp`, `hey`, and `gh`, so install and sign in to those three
first.

Any OpenAI-compatible gateway works: OpenAI, OpenRouter, LiteLLM, Ollama,
anything that speaks the chat completions wire format with streaming tools.

One prompt, all three apps:

```
> check my HEY imbox for anything from the team, turn action items into
  basecamp todos, and file code bugs as github issues
```

The agent reads the hey skill, checks the mailbox, decides which items need
action, then does the work through the basecamp and gh CLIs. Every step shows
up in the terminal and is saved in `runs/` so you can review it after.

## Reliability and evaluation

Retries with backoff cover flaky gateways (429, 5xx, dropped connections,
`Retry-After`) without losing the conversation, an idle watchdog catches
silent streams, and shell calls get hard timeouts and output caps. Every run
writes a JSONL transcript under `runs/`, and `evals/eval.sh` runs scripted
scenarios against the live model and asserts on those transcripts, all four
passing as of submission. See
[docs/reliability.md](docs/reliability.md) for the full write-up.

## Configuration

Everything lives in `.env` (see `.env.example`), real environment variables
still win:

```
AGENT_BASE_URL   OpenAI-compatible endpoint (required)
AGENT_API_KEY    bearer token (optional for local gateways)
AGENT_MODEL      model name (required)
AGENT_EFFORT     optional reasoning effort: minimal, low, medium, high, xhigh, max
```

## Notes

- Hand-coded with LLM assistance, about 4 hours start to finish.
- The shell tool runs POSIX `sh`, so Linux and macOS work out of the box.
- Skills follow the [Agent Skills](https://agentskills.io) format, the same
  standard other coding agents read, which means the integrations you write
  for this agent keep working elsewhere.
- This is a single-operator agent by design, no approval gate. It runs with
  the permissions of whoever starts it, in the working directory they choose.
