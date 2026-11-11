// Package ui is the browser side of Traffic Light: a page, its stylesheet, a handful of ES
// modules and two fonts, with no build step. They are built into the binary, and
// internal/output/web serves them.
package ui

import "embed"

// Files is everything a browser loads. jsconfig.json is only for type checking and stays out
//
//go:embed index.html login.html style.css logo.png favicon.ico js fonts
var Files embed.FS
