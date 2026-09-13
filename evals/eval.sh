#!/bin/sh
# Eval harness: run every scenario in evals/scenarios in one-shot mode and
# assert on the final output and the JSONL transcript.
#
#   evals/eval.sh              run all scenarios
#   evals/eval.sh name1 name2  run a subset by scenario name
#
# Assertions per scenario JSON:
#   expect_tools             tool names that must appear as tool_call events
#   expect_no_tool_calls     true = the run must finish without any tool call
#   expect_output_contains   substrings the final stdout must contain
#                            (keep them space-free, one token each)
#
# Scenarios hit the live model (and live CLIs), so a full run takes a few
# minutes and needs a working .env. Some scenarios assert on the author's
# account (the basecamp one expects a project named Omarchy), adjust the
# expectations in the scenario JSON for your own. Exit code 0 only when
# every assertion in every selected scenario passes.

set -u

cd "$(dirname "$0")/.." || exit 1
BIN=${BIN:-./agent-hackathon-go26}
if [ ! -x "$BIN" ]; then
  go build -o "$BIN" . || exit 1
fi

SELECT=$(IFS=,; echo "$*")
overall=0

for file in evals/scenarios/*.json; do
  name=$(grep -o '"name"[^,]*' "$file" | head -1 | cut -d'"' -f4)
  case ",$SELECT," in
    *",$name,"*) ;;
    ,,) ;;
    *) continue ;;
  esac

  prompt=$(grep -o '"prompt"[^}]*' "$file" | head -1 | sed 's/^"prompt"[: ]*"//; s/"$//')

  out=$(mktemp) || exit 1
  needle_file=$(mktemp) || exit 1
  before=$(ls runs 2>/dev/null | wc -l | tr -d ' ')
  "$BIN" "$prompt" >"$out" 2>&1
  status=$?
  after=$(ls runs 2>/dev/null | wc -l | tr -d ' ')

  transcript=""
  if [ "$after" -gt "$before" ]; then
    transcript=$(ls -t runs/*.jsonl 2>/dev/null | head -1)
  fi

  failed=0

  # expect_tools: each name must appear as a tool_call event
  # (key order in JSONL is alphabetical, so match on two greps, one line)
  tools=$(grep -o '"expect_tools"[^]]*]' "$file" | grep -o '"[a-z_]*"' | tr -d '"' | grep -v '^expect_tools$')
  for tool in $tools; do
    if [ -n "$transcript" ] && grep '"type":"tool_call"' "$transcript" | grep -q '"name":"'$tool'"'; then
      :
    else
      echo "FAIL $name: expected tool call to $tool"
      failed=1
    fi
  done

  # expect_no_tool_calls
  if grep -q '"expect_no_tool_calls": *true' "$file"; then
    if [ -n "$transcript" ] && grep -q '"type":"tool_call"' "$transcript"; then
      echo "FAIL $name: expected no tool calls"
      failed=1
    fi
  fi

  # expect_output_contains: redirect, not pipe, so failures propagate
  grep -o '"expect_output_contains"[^]]*]' "$file" | grep -o '"[^"]*"' | tr -d '"' | grep -v '^expect_output_contains$' >"$needle_file"
  while IFS= read -r needle; do
    [ -z "$needle" ] && continue
    if grep -qF -- "$needle" "$out"; then
      :
    else
      echo "FAIL $name: output missing: $needle"
      failed=1
    fi
  done <"$needle_file"

  if [ "$status" -ne 0 ]; then
    echo "FAIL $name: exit code $status"
    failed=1
  fi

  if [ "$failed" -eq 0 ]; then
    echo "PASS $name"
  else
    overall=1
  fi

  rm -f "$out" "$needle_file"
done

if [ "$overall" -eq 0 ]; then
  echo "all scenarios passed"
else
  echo "some scenarios failed"
fi
exit $overall
