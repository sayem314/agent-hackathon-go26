---
name: github
description: Work with GitHub via the gh CLI. Issues, pull requests, repos, runs, releases, search. Use for ANY GitHub read or action.
---

# GitHub

Everything GitHub through the `gh` CLI, already authenticated. Issues, pull
requests, repositories, Actions runs, releases, search, and raw API calls via
`gh api`.

## Workflow

1. Target a repo with `-R owner/name` or run inside the repo directory.
2. Read before writing: `gh issue list`, `gh pr list`, `gh run list`.
3. Issues and PRs open in the default browser unless a body/title is given on
   the command line. Always pass full fields so commands stay non-interactive:
   `gh issue create -t "title" -b "body"`.

## Common commands

```sh
gh repo list --limit 20
gh repo view owner/name
gh issue list -R owner/name --state open
gh issue view 42 -R owner/name --comments
gh issue create -R owner/name -t "Title" -b "Body"
gh issue comment 42 -b "text"
gh issue close 42
gh pr list -R owner/name
gh pr view 17 --comments
gh pr create -t "Title" -b "Body"
gh pr checks 17
gh pr merge 17 --squash
gh run list --limit 10
gh run view <id> --log-failed
gh search issues "memory leak" --repo owner/name
gh api repos/owner/name/contents/README.md --jq .content
```

## Notes

- Prefer `--jq` for API responses over parsing with other tools.
- For anything else: `gh <command> --help`.
