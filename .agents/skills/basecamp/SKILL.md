---
name: basecamp
description: Interact with Basecamp via the basecamp CLI. Projects, todos, cards, messages, chat, files, check-ins, schedule, search. Use for ANY Basecamp read or action.
---

# Basecamp

Full API coverage through the `basecamp` CLI: projects, todos, todolists, cards,
messages, chat (campfires), files and documents, check-ins, schedule, templates,
webhooks and more. Use it for any Basecamp question or action.

## Workflow

1. Find the project first when the user names one: `basecamp projects` then pass
   `-p <id-or-name>` on every following call. Without `-p` commands act on the
   default project.
2. List before you act: `basecamp todos list` or `basecamp cards list` to find
   the right item ID.
3. Confirm destructive intent (trash, archive) only when the user was ambiguous.

## Common commands

```sh
basecamp projects                     # list projects (use -p <name> afterwards)
basecamp todos list                   # todos in the default project
basecamp todos create "Write the proposal" --due 2026-09-20
basecamp todos complete <id>
basecamp cards list                   # kanban cards
basecamp cards create --title "Bug" --column <column-id>
basecamp cards move <card-id> --column <column-id>
basecamp messages list                # message board
basecamp chat lines "hello team"      # post to a campfire
basecamp search "quarterly review"
basecamp show <id-or-url>             # any item by ID or URL
```

## Output

- Default output is human-readable, `-j/--json` gives raw JSON, `--jq '<expr>'`
  filters it, `-m/--md` gives portable markdown.
- Prefer `--jq` over post-processing with python, and `-q` when another command
  consumes the value.
- Anything not listed here: `basecamp <command> --help`, or `basecamp commands`
  for the full command tree.
