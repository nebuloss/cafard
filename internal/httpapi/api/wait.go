package api

import (
	"net/http"
	"strconv"
	"time"

	"cafard/internal/service/access"
	"cafard/internal/service/attention"
	"cafard/internal/store"
)

// wait is MCP's wait, for a client that is not an MCP client: mouchard's
// channel, which wakes a Claude Code session when something is addressed to
// its agent. It blocks until something addressed to the caller arrives in
// the project, or timeout seconds pass (50 at most), and answers what
// arrived. What is addressed is service/attention's to say, as for inbox.
//
//	GET /api/v1/wait?project=be98&timeout=50
//	{"arrived":[{"conversation":"build-42","reason":"mentioned",…}],"waiting":1}
func (a *API) wait(w http.ResponseWriter, r *http.Request, who access.Who) {
	if a.att == nil {
		writeError(w, http.StatusNotFound, "not_found", say(r, "This server cannot wait."))
		return
	}
	name := r.URL.Query().Get("project")
	ps, err := a.chat.Projects(r.Context(), who)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var p *store.Project
	for i := range ps {
		if ps[i].Name == name {
			p = &ps[i]
			break
		}
	}
	if p == nil {
		a.fail(w, r, access.Refuse(access.NotFound, "No project %q among yours.", name))
		return
	}
	secs, _ := strconv.Atoi(r.URL.Query().Get("timeout"))
	fresh, waiting, err := a.att.Wait(r.Context(), who, *p, time.Duration(secs)*time.Second)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	type entry struct {
		Conversation string    `json:"conversation"`
		Title        string    `json:"title"`
		Reason       string    `json:"reason"`
		By           string    `json:"by"`
		ByKind       string    `json:"by_kind"`
		Quote        string    `json:"quote,omitempty"`
		New          int       `json:"new"`
		Files        int       `json:"files,omitempty"`
		Images       int       `json:"images,omitempty"`
		Tokens       int       `json:"tokens"`
		Message      int64     `json:"message"`
		At           time.Time `json:"at"`
	}
	out := []entry{}
	for _, e := range fresh {
		out = append(out, entry{Conversation: e.Conversation.Name, Title: e.Conversation.Title, Reason: string(e.Reason),
			By: e.By.AuthorName, ByKind: string(e.By.AuthorKind), Quote: attention.Quote(e.By.Text, 200),
			New: e.New, Files: e.Files, Images: e.Images, Tokens: e.Tokens, Message: e.Latest, At: e.By.CreatedAt.UTC()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": name, "arrived": out, "waiting": len(waiting)})
}
