#!/bin/bash

set -euo pipefail

# Script to build Go bindings for bdk-ffi
# This script builds the Rust library and generates Go bindings using uniffi-bindgen-go

TARGETDIR="../bdk-ffi/target"
OUTDIR="."
NAME="bdkffi"
PROFILE="release"

# Detect host architecture
HOST_ARCH=$(uname -m)
HOST_OS=$(uname -s)

if [ "$HOST_OS" = "Darwin" ]; then
    if [ "$HOST_ARCH" = "arm64" ]; then
        TARGET="aarch64-apple-darwin"
        LIB_EXT="dylib"
    else
        TARGET="x86_64-apple-darwin"
        LIB_EXT="dylib"
    fi
elif [ "$HOST_OS" = "Linux" ]; then
    if [ "$HOST_ARCH" = "aarch64" ]; then
        TARGET="aarch64-unknown-linux-gnu"
        LIB_EXT="so"
    else
        TARGET="x86_64-unknown-linux-gnu"
        LIB_EXT="so"
    fi
elif [ "$HOST_OS" = "MINGW64_NT" ] || [ "$HOST_OS" = "MSYS_NT" ]; then
    TARGET="x86_64-pc-windows-msvc"
    LIB_EXT="dll"
else
    echo "Unsupported OS: $HOST_OS"
    exit 1
fi

LIB_NAME="lib${NAME}.${LIB_EXT}"
STATIC_LIB_NAME="lib${NAME}.a"

cd ../bdk-ffi/ || exit

echo "Building Rust library for target: $TARGET"

# Build the Rust library
cargo build --package bdk-ffi --profile "$PROFILE" --target "$TARGET"

echo "Generating Go bindings..."

# Create output directory in bdk-go
mkdir -p ../bdk-go/bdk

# Generate Go bindings using uniffi-bindgen-go
# Use --library mode to extract metadata from the built library
# Run from bdk-ffi directory so cargo metadata can find Cargo.toml
uniffi-bindgen-go \
    --library \
    --crate bdk-ffi \
    --config ./uniffi.toml \
    --out-dir ../bdk-go/bdk \
    --no-format \
    "./target/$TARGET/$PROFILE/$LIB_NAME"

cd ../bdk-go/ || exit

echo "Go bindings generated in ./bdk/"
echo "Static library available at: ../bdk-ffi/target/$TARGET/$PROFILE/$STATIC_LIB_NAME"

