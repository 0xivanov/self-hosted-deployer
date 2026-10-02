#!/bin/sh
set -eu

PROJECT_ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
PROVENANCE=$PROJECT_ROOT/scripts/build-provenance.sh
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/deployer-provenance-test.XXXXXX")
trap 'rm -rf "$ROOT"' EXIT INT TERM
REPOSITORY=$ROOT/repository
SUBMODULE_SOURCE=$ROOT/submodule-source

mkdir -p "$REPOSITORY" "$SUBMODULE_SOURCE"
cd "$SUBMODULE_SOURCE"
git init -q
git config user.name provenance-test
git config user.email provenance-test@example.invalid
printf 'submodule tracked\n' >tracked.txt
git add tracked.txt
git commit -q -m initial

cd "$REPOSITORY"
git init -q
git config user.name provenance-test
git config user.email provenance-test@example.invalid
mkdir -p nested
printf 'tracked\n' >tracked.txt
printf 'nested\n' >nested/tracked.txt
git -c protocol.file.allow=always submodule add -q \
  "$SUBMODULE_SOURCE" vendor/provenance-submodule
git add tracked.txt nested/tracked.txt .gitmodules vendor/provenance-submodule
git commit -q -m initial
revision=$(git rev-parse HEAD)

resolved=$("$PROVENANCE" resolve)
[ "$resolved" = "$revision" ] || {
  echo "clean source resolved as $resolved, want $revision" >&2
  exit 1
}
EXPECTED_COMMIT=$revision "$PROVENANCE" require-clean >/dev/null

cd nested
resolved=$("$PROVENANCE" resolve)
[ "$resolved" = "$revision" ] || {
  echo "nested invocation resolved as $resolved, want $revision" >&2
  exit 1
}
cd ..

git tag v1.2.3
git switch --detach -q v1.2.3
resolved=$(EXPECTED_COMMIT=$revision "$PROVENANCE" resolve)
[ "$resolved" = "$revision" ] || {
  echo "clean detached tag resolved as $resolved, want $revision" >&2
  exit 1
}
EXPECTED_COMMIT=$revision "$PROVENANCE" require-clean >/dev/null

git update-index --assume-unchanged tracked.txt
if "$PROVENANCE" resolve >"$ROOT/assume-unchanged.out" 2>&1; then
  echo "assume-unchanged index entry unexpectedly resolved as clean" >&2
  exit 1
fi
grep -q 'refuses Git index entries marked assume-unchanged or skip-worktree' \
  "$ROOT/assume-unchanged.out"
if "$PROVENANCE" require-clean >"$ROOT/assume-unchanged-clean.out" 2>&1; then
  echo "assume-unchanged index entry unexpectedly passed the production gate" >&2
  exit 1
fi
grep -q 'refuses Git index entries marked assume-unchanged or skip-worktree' \
  "$ROOT/assume-unchanged-clean.out"
git update-index --no-assume-unchanged tracked.txt

git update-index --skip-worktree tracked.txt
if "$PROVENANCE" resolve >"$ROOT/skip-worktree.out" 2>&1; then
  echo "skip-worktree index entry unexpectedly resolved as clean" >&2
  exit 1
fi
grep -q 'refuses Git index entries marked assume-unchanged or skip-worktree' \
  "$ROOT/skip-worktree.out"
if "$PROVENANCE" require-clean >"$ROOT/skip-worktree-clean.out" 2>&1; then
  echo "skip-worktree index entry unexpectedly passed the production gate" >&2
  exit 1
fi
grep -q 'refuses Git index entries marked assume-unchanged or skip-worktree' \
  "$ROOT/skip-worktree-clean.out"
git update-index --no-skip-worktree tracked.txt

git -C vendor/provenance-submodule update-index --assume-unchanged tracked.txt
if "$PROVENANCE" resolve >"$ROOT/submodule-assume-unchanged.out" 2>&1; then
  echo "submodule assume-unchanged entry unexpectedly resolved as clean" >&2
  exit 1
fi
grep -q 'refuses Git index entries marked assume-unchanged or skip-worktree in submodule' \
  "$ROOT/submodule-assume-unchanged.out"
git -C vendor/provenance-submodule update-index --no-assume-unchanged tracked.txt

git -C vendor/provenance-submodule update-index --skip-worktree tracked.txt
if "$PROVENANCE" require-clean >"$ROOT/submodule-skip-worktree.out" 2>&1; then
  echo "submodule skip-worktree entry unexpectedly passed the production gate" >&2
  exit 1
fi
grep -q 'refuses Git index entries marked assume-unchanged or skip-worktree in submodule' \
  "$ROOT/submodule-skip-worktree.out"
git -C vendor/provenance-submodule update-index --no-skip-worktree tracked.txt

wrong=$(printf '%040d' 0)
if EXPECTED_COMMIT=$wrong "$PROVENANCE" resolve >"$ROOT/wrong.out" 2>&1; then
  echo "mismatched expected commit unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'does not match checked-out commit' "$ROOT/wrong.out"

printf 'changed\n' >>tracked.txt
resolved=$(EXPECTED_COMMIT=$revision "$PROVENANCE" resolve)
[ "$resolved" = "$revision-dirty" ] || {
  echo "dirty tracked source resolved as $resolved" >&2
  exit 1
}
if "$PROVENANCE" require-clean >"$ROOT/tracked-dirty.out" 2>&1; then
  echo "dirty tracked source unexpectedly passed the production gate" >&2
  exit 1
fi
grep -q 'production builds require a clean Git worktree' "$ROOT/tracked-dirty.out"

git restore tracked.txt
printf 'untracked\n' >untracked.txt
resolved=$("$PROVENANCE" resolve)
[ "$resolved" = "$revision-dirty" ] || {
  echo "untracked source resolved as $resolved" >&2
  exit 1
}
if "$PROVENANCE" require-clean >"$ROOT/untracked-dirty.out" 2>&1; then
  echo "untracked source unexpectedly passed the production gate" >&2
  exit 1
fi

mv untracked.txt "$ROOT/untracked.txt"
printf 'ignored-source.go\n' >.git/info/exclude
printf 'ignored source\n' >ignored-source.go
resolved=$("$PROVENANCE" resolve)
[ "$resolved" = "$revision-dirty" ] || {
  echo "ignored source resolved as $resolved" >&2
  exit 1
}
if "$PROVENANCE" require-clean >"$ROOT/ignored-dirty.out" 2>&1; then
  echo "ignored source unexpectedly passed the production gate" >&2
  exit 1
fi

if make --no-print-directory -C "$PROJECT_ROOT" -n COMMIT="$revision" build >"$ROOT/override.out" 2>&1; then
  echo "legacy COMMIT override unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'COMMIT cannot be overridden' "$ROOT/override.out"

make_repository=$ROOT/make-repository
mkdir -p "$make_repository/scripts"
cp "$PROJECT_ROOT/Makefile" "$make_repository/Makefile"
cp "$PROVENANCE" "$make_repository/scripts/build-provenance.sh"
cd "$make_repository"
git init -q
git config user.name provenance-test
git config user.email provenance-test@example.invalid
git add Makefile scripts/build-provenance.sh
git commit -q -m initial
make_revision=$(git rev-parse HEAD)
make_output=$ROOT/make-bin
make --no-print-directory GO=true BIN_DIR="$make_output" \
  EXPECTED_COMMIT="$make_revision" build >/dev/null
if make --no-print-directory GO=true BIN_DIR="$make_output" \
  EXPECTED_COMMIT="$wrong" build >"$ROOT/make-mismatch.out" 2>&1; then
  echo "make build ignored a provenance resolver failure" >&2
  exit 1
fi
grep -q 'does not match checked-out commit' "$ROOT/make-mismatch.out"

binary_repository=$ROOT/binary-repository
mkdir -p "$binary_repository/internal/version" "$binary_repository/cmd/provenance-fixture"
cp "$PROJECT_ROOT/internal/version/version.go" "$binary_repository/internal/version/version.go"
cat >"$binary_repository/go.mod" <<'EOF'
module github.com/0xivanov/self-hosted-deployer

go 1.26.5
EOF
cat >"$binary_repository/cmd/provenance-fixture/main.go" <<'EOF'
package main

import (
	"fmt"

	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

func main() {
	fmt.Println(version.Current().Commit)
}
EOF
cd "$binary_repository"
git init -q
git config user.name provenance-test
git config user.email provenance-test@example.invalid
git add go.mod internal/version/version.go cmd/provenance-fixture/main.go
git commit -q -m initial
binary_revision=$(git rev-parse HEAD)
git tag v1.2.3
git switch --detach -q v1.2.3

build_fixture() {
  output=$1
  embedded=$2
  build_vcs=$3
  GOWORK=off CGO_ENABLED=0 go build -buildvcs="$build_vcs" \
    -ldflags "-X github.com/0xivanov/self-hosted-deployer/internal/version.Commit=$embedded" \
    -o "$output" ./cmd/provenance-fixture
}

build_fixture "$ROOT/clean-binary" "$binary_revision" true
reported=$("$ROOT/clean-binary")
[ "$reported" = "$binary_revision" ] || {
  echo "clean detached binary reported $reported, want $binary_revision" >&2
  exit 1
}

printf '\n// tracked source change\n' >>cmd/provenance-fixture/main.go
build_fixture "$ROOT/tracked-dirty-binary" "$binary_revision-dirty" true
reported=$("$ROOT/tracked-dirty-binary")
[ "$reported" = "$binary_revision-dirty" ] || {
  echo "tracked dirty binary reported $reported" >&2
  exit 1
}

git restore cmd/provenance-fixture/main.go
cat >cmd/provenance-fixture/untracked.go <<'EOF'
package main

const untrackedSourceIsCompiled = true
EOF
build_fixture "$ROOT/untracked-dirty-binary" "$binary_revision-dirty" true
reported=$("$ROOT/untracked-dirty-binary")
[ "$reported" = "$binary_revision-dirty" ] || {
  echo "untracked dirty binary reported $reported" >&2
  exit 1
}

build_fixture "$ROOT/spoofed-binary" "$binary_revision" true
reported=$("$ROOT/spoofed-binary")
[ "$reported" = "$binary_revision-dirty-mismatch" ] || {
  echo "dirty binary with a spoofed clean ldflag reported $reported" >&2
  exit 1
}

build_fixture "$ROOT/unverified-binary" "$binary_revision" false
reported=$("$ROOT/unverified-binary")
[ "$reported" = "$binary_revision-unverified" ] || {
  echo "binary without Go VCS metadata reported $reported" >&2
  exit 1
}

echo "build provenance tests passed"
