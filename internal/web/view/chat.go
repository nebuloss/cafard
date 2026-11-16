package view

import (
	"net/url"
	"strings"
)

// ChatHref is the page of a chat id ("chan:x" or "dm:peer").
func ChatHref(id string) string {
	if name, ok := strings.CutPrefix(id, "chan:"); ok {
		return "/c/" + url.PathEscape(name)
	}
	if peer, ok := strings.CutPrefix(id, "dm:"); ok {
		return "/dm/" + url.PathEscape(peer)
	}
	return "/"
}

// ChatLabel names a chat id ("chan:dut1" -> "#dut1").
func ChatLabel(id string) string {
	if name, ok := strings.CutPrefix(id, "chan:"); ok {
		return "#" + name
	}
	if peer, ok := strings.CutPrefix(id, "dm:"); ok {
		return "@" + peer
	}
	return id
}
