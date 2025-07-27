#!/bin/bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o dist/ccw ./main.go
upx --best --lzma dist/ccw