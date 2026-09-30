package dev

import (
	"embed"
	"io"
	"os"
	"path/filepath"
	"text/template"

	"al.essio.dev/pkg/shellescape"
	"github.com/creachadair/atomicfile"
)

//go:embed templates/*
var templateFiles embed.FS

var fileTemplates = template.Must(template.New("").Funcs(template.FuncMap{
	"shellQuote":      shellescape.Quote,
	"shellCommand":    shellescape.QuoteCommand,
	"sshQuote":        sshConfigQuote,
	"escapeSSHTokens": escapeSSHTokens,
}).ParseFS(templateFiles, "templates/*"))

// writeTemplate renders an embedded template into an atomically replaced private file.
func writeTemplate(destinationPath, templateName string, data any) error {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0700); err != nil {
		return err
	}
	return atomicfile.Tx(destinationPath, 0600, func(output io.Writer) error {
		return fileTemplates.ExecuteTemplate(output, templateName, data)
	})
}
