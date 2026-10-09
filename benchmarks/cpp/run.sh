#!/bin/sh
# Clones mFAST at a pinned commit, builds it and the harness in Release, runs.
set -eu
cd "$(dirname "$0")"
REPO=https://github.com/objectcomputing/mFAST.git
REF=3bab3965d4bd5769c57309f180e358f1528ee74a   # master, 2025-12-09 (after v1.2.2)
SRC=third_party/mFAST
if [ ! -d "$SRC/.git" ]; then
  git clone -q "$REPO" "$SRC"
fi
git -C "$SRC" checkout -q "$REF"
git -C "$SRC" submodule update -q --init tinyxml2   # pinned by mFAST's own tree
# No source patches needed. CMake Release = -O3 -DNDEBUG (no -march flags).
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release >/dev/null
cmake --build build -j "$(sysctl -n hw.ncpu 2>/dev/null || echo 4)" --target mfast_bench >/dev/null
echo "$(c++ --version | head -1); flags: -O3 -DNDEBUG (CMake Release; compiler-default C++ std, arm64)"
exec ./build/mfast_bench ../testdata
