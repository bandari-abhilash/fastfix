#!/bin/sh
# Builds (release, lto, codegen-units=1) and runs the fastlib benchmark.
set -eu
cd "$(dirname "$0")"
cargo build --release --quiet
echo "$(rustc --version)"
exec ./target/release/fastlib-bench ../testdata
