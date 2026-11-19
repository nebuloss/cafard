package view

import (
	"net/url"
	"strings"
)

// The addresses of the project view (DECISIONS 2026-11-19, "projects
// first"). src/render/format.ts mirrors each builder.

// ProjectHref is a project's main view, which opens its latest chat.
func ProjectHref(project string) string { return "/projects/" + url.PathEscape(project) }

// SettingsHref is a project's settings, on the admin interface.
func SettingsHref(project string) string { return ProjectHref(project) + "/settings" }

// GalleriesHref lists a project's galleries.
func GalleriesHref(project string) string { return ProjectHref(project) + "/g" }

// LogsHref is the live logs of a project's sinks, or of one sink when
// sink is not "".
func LogsHref(project, sink string) string {
	h := ProjectHref(project) + "/logs"
	if sink != "" {
		h += "/" + url.PathEscape(sink)
	}
	return h
}

// ChatHref is the page of a chat id ("chan:lab/x" or "dm:peer").
func ChatHref(id string) string {
	if name, ok := strings.CutPrefix(id, "chan:"); ok {
		project, local, _ := strings.Cut(name, "/")
		return ProjectHref(project) + "/c/" + url.PathEscape(local)
	}
	if peer, ok := strings.CutPrefix(id, "dm:"); ok {
		return "/dm/" + url.PathEscape(peer)
	}
	return "/"
}

// ChatLabel names a chat id ("chan:lab/dut1" -> "#lab/dut1").
func ChatLabel(id string) string {
	if name, ok := strings.CutPrefix(id, "chan:"); ok {
		return "#" + name
	}
	if peer, ok := strings.CutPrefix(id, "dm:"); ok {
		return "@" + peer
	}
	return id
}
