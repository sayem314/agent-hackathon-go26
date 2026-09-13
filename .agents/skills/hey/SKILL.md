---
name: hey
description: Interact with HEY via the hey CLI. Read and send email, reply, forward, screen senders, calendar, todos, contacts. Use for ANY HEY email question or action.
---

# HEY

Email and more through the `hey` CLI: read, search, reply, compose, forward,
screen senders, labels, collections, calendar, todos, journal. Use it for any
HEY question or action.

## Workflow

1. Discover thread IDs with `hey box` (Imbox and other boxes) or `hey search`.
2. Read with `hey thread read <thread-id>` before replying or forwarding.
3. Replying: `hey reply <thread-id> -m "text"`. Composing new mail:
   `hey compose --to a@b.com --subject "..." -m "..."`. Add `--draft` to stop
   before sending when the user has not approved the wording yet.

## Common commands

```sh
hey box                     # list boxes and their threads
hey box imbox               # the Imbox
hey search "invoice"        # search threads and messages
hey thread read <id>        # read one thread
hey reply <id> -m "..."     # reply to a thread
hey compose --to x@y.z --subject "S" -m "body"
hey forward <id> --to x@y.z
hey screener                # decide who may email you
hey seen <id> | unseen <id> | trash <id> | move <id> --box <box>
hey label list              # labels
hey calendar list           # calendars
hey event list              # events
hey todo list               # to-dos
```

## Notes

- Bodies accept markdown via `-m/--message`.
- IDs are HEY thread IDs as printed by `hey box`/`hey search`, not email
  message IDs.
- Anything not listed here: `hey <command> --help`.
