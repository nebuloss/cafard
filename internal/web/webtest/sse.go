package webtest

import (
	"bufio"
	"io"
	"strings"
)

// Event is one event of /api/events; a comment (": ping") has Comment
// set and its text in Data.
type Event struct {
	Name, ID, Data string
	Comment        bool
}

// ReadEvents parses an event stream until it ends, then closes body and
// the channel.
func ReadEvents(body io.ReadCloser) <-chan Event {
	out := make(chan Event, 1000)
	go func() {
		defer close(out)
		defer body.Close()
		sc := bufio.NewScanner(body)
		var ev Event
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.Name != "" || ev.Comment {
					out <- ev
				}
				ev = Event{}
			case strings.HasPrefix(line, ": "):
				ev.Comment, ev.Data = true, line[2:]
			case strings.HasPrefix(line, "event: "):
				ev.Name = line[7:]
			case strings.HasPrefix(line, "id: "):
				ev.ID = line[4:]
			case strings.HasPrefix(line, "data: "):
				ev.Data = line[6:]
			}
		}
	}()
	return out
}
