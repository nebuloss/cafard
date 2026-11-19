package view

import (
	"net/url"
	"strings"
)

// ChatHref is the page of a chat id ("chan:lab/x" or "dm:peer").
func ChatHref(id string) string {
	if name, ok := strings.CutPrefix(id, "chan:"); ok {
		project, local, _ := strings.Cut(name, "/")
		return "/c/" + url.PathEscape(project) + "/" + url.PathEscape(local)
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
