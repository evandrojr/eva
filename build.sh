#!/bin/bash
rm -f eva
go clean -cache
GOPROXY=off go build -o eva .