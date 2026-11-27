#!/bin/sh
# Starts the real server for the end-to-end tests with stand-ins for the
# external tools: a converter that copies files, a folder acting as the
# Kindle and, built in with the fakemega tag, a stand-in for MEGA whose
# cloud drive is another folder, built in with the fakemodel tag, a
# stand-in for the language model that restores books and, built in with
# the fakecovers tag, a stand-in for the websites that covers are found
# on. Converting to EPUB for a preview yields the almanac fixture,
# whatever the book.
set -eu
cd "$(dirname "$0")/.."

PORT="${ATHENAEUM_E2E_PORT:-8091}"
WORK="$PWD/e2e/.work"

rm -rf "$WORK"
mkdir -p "$WORK/bin" "$WORK/fixtures" "$WORK/kindle/documents" "$WORK/mega"
go build -tags "fakemega fakemodel fakecovers" -o "$WORK/bin/athenaeum" .
go run ./testdata/fixtures "$WORK/fixtures"
cp testdata/bin/ebook-convert "$WORK/bin/ebook-convert"

export FAKE_MEGA_ROOT="$WORK/mega"
export FAKE_CONVERT_EPUB="$WORK/fixtures/almanac.epub"
export FAKE_MEGA_DELAY_FILE="$WORK/mega-delay"
export FAKE_MODEL_DECLINE_FILE="$WORK/model-decline"
export ATHENAEUM_ADDR=":$PORT"
export ATHENAEUM_HOME="$WORK/data"
export ATHENAEUM_KINDLE_PATH="$WORK/kindle"
export ATHENAEUM_EBOOK_CONVERT="$WORK/bin/ebook-convert"
export ATHENAEUM_EBOOK_META="$WORK/bin/no-ebook-meta"
exec "$WORK/bin/athenaeum"
