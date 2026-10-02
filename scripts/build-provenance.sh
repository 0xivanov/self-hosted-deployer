#!/bin/sh
set -eu

usage() {
  echo "usage: $0 resolve|require-clean" >&2
  exit 2
}

[ "$#" -eq 1 ] || usage
mode=$1
case "$mode" in
  resolve|require-clean) ;;
  *) usage ;;
esac

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  echo "build provenance requires a Git worktree" >&2
  exit 1
}
worktree=$(git rev-parse --show-toplevel 2>/dev/null) || {
  echo "build provenance could not resolve the Git worktree root" >&2
  exit 1
}
cd "$worktree"

revision=$(git rev-parse --verify 'HEAD^{commit}' 2>/dev/null) || {
  echo "build provenance could not resolve HEAD" >&2
  exit 1
}
case "$revision" in
  *[!0-9a-f]*)
    echo "build provenance resolved an invalid Git object ID" >&2
    exit 1
    ;;
esac
case "${#revision}" in
  40|64) ;;
  *)
    echo "build provenance resolved an invalid Git object ID" >&2
    exit 1
    ;;
esac

expected=${EXPECTED_COMMIT:-}
if [ -n "$expected" ] && [ "$expected" != "$revision" ]; then
  echo "expected commit $expected does not match checked-out commit $revision" >&2
  exit 1
fi

index_entries=$(git ls-files -v --) || {
  echo "build provenance could not inspect Git index flags" >&2
  exit 1
}
hidden_index_entries=$(printf '%s\n' "$index_entries" | LC_ALL=C sed -n '/^[a-zS] /p')
if [ -n "$hidden_index_entries" ]; then
  echo "build provenance refuses Git index entries marked assume-unchanged or skip-worktree" >&2
  exit 1
fi
if ! git submodule foreach --quiet --recursive '
  index_entries=$(git ls-files -v --) || exit 1
  hidden_index_entries=$(printf "%s\n" "$index_entries" | LC_ALL=C sed -n "/^[a-zS] /p")
  if [ -n "$hidden_index_entries" ]; then
    echo "build provenance refuses Git index entries marked assume-unchanged or skip-worktree in submodule $displaypath" >&2
    exit 1
  fi
'; then
  echo "build provenance could not inspect Git submodule index flags" >&2
  exit 1
fi

status=$(git status --porcelain=v1 --untracked-files=normal --ignore-submodules=none) || {
  echo "build provenance could not inspect the Git worktree" >&2
  exit 1
}
ignored=$(git ls-files --others --ignored --exclude-standard -- . \
  ':(exclude)bin' ':(exclude)bin/**' \
  ':(exclude)dist' ':(exclude)dist/**') || {
  echo "build provenance could not inspect ignored worktree files" >&2
  exit 1
}

if [ -n "$status" ] || [ -n "$ignored" ]; then
  if [ "$mode" = require-clean ]; then
    echo "production builds require a clean Git worktree, including submodules, untracked files, and ignored files outside bin and dist" >&2
    exit 1
  fi
  printf '%s-dirty\n' "$revision"
  exit 0
fi

printf '%s\n' "$revision"
