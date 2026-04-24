#!/bin/bash
set -e

echo "Installing dev dependencies..."

go install github.com/air-verse/air@latest

echo "dev dependencies installed!"
echo "Run 'make dev-reload' to start dev mode with hot reload."