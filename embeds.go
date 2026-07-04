package main

import "embed"

//go:embed static/index.html static/manifest.webmanifest static/sw.js static/css/* static/js/* static/icon/* sql/init.sql
var embeddedFiles embed.FS
