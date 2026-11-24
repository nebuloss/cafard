package admin

import (
	"fmt"
	"net/http"
	"time"

	"cafard/internal/service/access"
)

// Event stream timing, as the chat's (internal/httpapi/api): notices are
// gathered for batchWindow, a comment every keepAlive keeps proxies from
// closing a quiet stream, and a stream ends after maxStream so the session
// is checked again when the browser opens the next one, as claudication's
// does.
const (
	batchWindow = 250 * time.Millisecond
	keepAlive   = 25 * time.Second
	maxStream   = 30 * time.Minute
)

// events is the admin UI's one stream of what changed: it replaces reading
// everything every minute. The admin sees everything, so the stream hears
// every key, and says only that something changed; the page reads again
// through the routes above.
//
//	event: changed
//	data: {}
//
// A stream that fell behind is told the same: it reads everything anyway.
func (a *Admin) events(w http.ResponseWriter, r *http.Request, who access.Who) {
	rc := http.NewResponseController(w)
	sub := a.cfg.Hub.Subscribe(nil)
	defer a.cfg.Hub.Unsubscribe(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\nevent: ready\ndata: {}\n\n")
	if rc.Flush() != nil {
		return
	}

	ctx := r.Context()
	alive := time.NewTicker(keepAlive)
	defer alive.Stop()
	end := time.NewTimer(maxStream)
	defer end.Stop()
	var batch <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-end.C:
			return
		case <-alive.C:
			fmt.Fprint(w, ": still here\n\n")
			rc.Flush()
		case _, open := <-sub.C:
			if !open {
				return // the server is stopping
			}
			if batch == nil {
				batch = time.After(batchWindow)
			}
		case <-batch:
			batch = nil
			fmt.Fprint(w, "event: changed\ndata: {}\n\n")
			rc.Flush()
		}
	}
}
