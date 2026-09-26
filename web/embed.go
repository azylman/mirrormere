package web

import "embed"

// Content embeds all dashboard HTML templates and web static assets (CSS, JS).
// This serves as an in-binary hermetic fallback when disk files are unmounted.
//
//go:embed templates/* static/*
var Content embed.FS
