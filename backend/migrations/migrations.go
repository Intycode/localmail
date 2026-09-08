// Package migrations embeds the SQLite schema so the database package can
// apply them at startup. Files here are the single source of the local schema.
package migrations

import (
	"embed"
	"sort"
)

//go:embed *.sql
var Files embed.FS

// Names returns the migration file names in lexical order.
func Names() []string {
	entries, err := Files.ReadDir(".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// Read returns the content of a migration file by name.
func Read(name string) (string, error) {
	b, err := Files.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
