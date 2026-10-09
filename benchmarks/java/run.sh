#!/bin/sh
# Downloads OpenFAST (no required runtime deps), compiles and runs the harness.
set -eu
cd "$(dirname "$0")"
VER=1.1.1
JAR=lib/openfast-$VER.jar
URL=https://repo1.maven.org/maven2/org/openfast/openfast/$VER/openfast-$VER.jar
SHA1=c30a14be32f50ec1eb99416f2f704c7fce69c051
if [ ! -f "$JAR" ]; then
  mkdir -p lib
  curl -fsSL -o "$JAR" "$URL"
fi
echo "$SHA1  $JAR" | shasum -a 1 -c - >/dev/null
rm -rf out && mkdir -p out
javac -nowarn -d out -cp "$JAR" src/OpenFastBench.java
JVM_FLAGS="-Xms1g -Xmx1g -XX:+UseParallelGC"
echo "java $(java -version 2>&1 | head -1); flags: $JVM_FLAGS"
exec java $JVM_FLAGS -cp "out:$JAR" OpenFastBench ../testdata
