// Package fileicons embeds unchanged Material Icon Theme v5.40.0 artwork.
// Render Light/Dark with ui.Image to preserve the upstream colors.
// Licenses and provenance ship in resources/licenses/material-icon-theme.
package fileicons

import (
	"embed"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

//go:embed svg/*.svg
var files embed.FS

// Icon is the original artwork for both application themes.
type Icon struct {
	Name        string
	Light, Dark *ui.SVG
}

var icons = loadIcons()

func loadIcons() map[string]*Icon {
	entries, err := files.ReadDir("svg")
	if err != nil {
		panic(err)
	}
	result := make(map[string]*Icon)
	available := make(map[string]bool, len(entries))
	for _, entry := range entries {
		available[entry.Name()] = true
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".svg")
		if strings.HasSuffix(name, "_light") {
			continue
		}
		dark := parse(name)
		light := dark
		if available[name+"_light.svg"] {
			light = parse(name + "_light")
		}
		result[name] = &Icon{Name: name, Light: light, Dark: dark}
	}
	for _, mappings := range []map[string]string{fileNames, extensions, folderNames} {
		for _, name := range mappings {
			if result[name] == nil {
				panic("fileicons: missing mapped icon " + name)
			}
		}
	}
	for _, name := range []string{"file", "folder"} {
		if result[name] == nil {
			panic("fileicons: missing default icon " + name)
		}
	}
	return result
}

func parse(name string) *ui.SVG {
	data, err := files.ReadFile("svg/" + name + ".svg")
	if err != nil {
		panic(fmt.Sprintf("fileicons: missing %s: %v", name, err))
	}
	return ui.MustParseSVG(data)
}

// Lookup matches a basename without filesystem or network access. Directories
// take priority; files match exact names before their longest compound suffix.
func Lookup(name string, isDir bool) *Icon {
	normalized := strings.ToLower(name)
	if isDir {
		if icon, ok := folderNames[normalized]; ok {
			return icons[icon]
		}
		return icons["folder"]
	}
	if icon, ok := fileNames[normalized]; ok {
		return icons[icon]
	}
	suffix := normalized
	for i := strings.IndexByte(suffix, '.'); i >= 0; {
		suffix = suffix[i+1:]
		if icon, ok := extensions[suffix]; ok {
			return icons[icon]
		}
		i = strings.IndexByte(suffix, '.')
	}
	return icons["file"]
}
