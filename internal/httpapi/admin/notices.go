package admin

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"cafard/internal/service/access"
)

// The notification centre, as claudication's: what needs the admin now, each
// a sentence and a link to where it is dealt with. Worked out from the state
// on every read; nothing is stored but which ones the admin marked read, and
// a mark is forgotten once its notice is gone, so a problem that comes back
// is news again.
//
//	GET  /admin/api/notices        {notices: [...]}, the worst first
//	POST /admin/api/notices/read   {ids, read?}: mark read, or unread

const (
	noticeError = "error"
	noticeWarn  = "warn"
	noticeInfo  = "info"
)

// notice is one thing that needs the admin.
type notice struct {
	// ID is stable while the problem lasts: the read mark is kept by it.
	ID       string `json:"id"`
	Severity string `json:"severity"`
	// Kind is what it is about: member or project.
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	// Link is the UI path where it is dealt with.
	Link  string     `json:"link,omitempty"`
	Since *time.Time `json:"since,omitempty"`
	Read  *time.Time `json:"read,omitempty"`
}

const (
	// unusedAfter is how long an agent or a device may go without
	// connecting before the admin hears of it: a machine that stopped, or
	// one nobody uses and whose token is still good.
	unusedAfter = 30 * 24 * time.Hour
	// nearlyFull is the share of its quota past which a project is close to
	// refusing uploads.
	nearlyFull = 0.8
)

var severityRank = map[string]int{noticeError: 0, noticeWarn: 1, noticeInfo: 2}

func (a *Admin) notices(r *http.Request, now time.Time) ([]notice, error) {
	out := []notice{}
	members, err := a.memberList(r)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if m.Removed {
			continue
		}
		link := "/members/" + m.Name
		if m.Link != nil && !m.Joined && m.Link.State == "expired" {
			what := "join"
			if m.Kind == "agent" {
				what = "install"
			}
			t := m.Link.ExpiresAt
			out = append(out, notice{ID: fmt.Sprintf("link-expired:%d", m.Link.ID), Severity: noticeWarn, Kind: "member",
				Title: fmt.Sprintf("The %s link for %s expired unused", what, m.Name), Detail: "Make a new one from its page.", Link: link, Since: &t})
		}
		if m.Kind != "person" && m.Enabled && m.LastUsed != nil && now.Sub(*m.LastUsed) > unusedAfter {
			days := int(now.Sub(*m.LastUsed).Hours() / 24)
			out = append(out, notice{ID: "unused:" + m.Name, Severity: noticeWarn, Kind: "member",
				Title:  fmt.Sprintf("%s has not connected for %d days", m.Name, days),
				Detail: "Its token still works. Pause it if the machine is gone.", Link: link, Since: m.LastUsed})
		}
		if m.Enabled && len(m.Projects) == 0 {
			out = append(out, notice{ID: "no-project:" + m.Name, Severity: noticeInfo, Kind: "member",
				Title: fmt.Sprintf("%s is in no project", m.Name), Detail: "It can reach nothing until it is put in one.", Link: link})
		}
	}
	projects, err := a.projectList(r)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if p.Archived {
			continue
		}
		link := "/projects/" + p.Name
		if p.QuotaBytes > 0 {
			share := float64(p.Bytes) / float64(p.QuotaBytes)
			switch {
			case share >= 1:
				out = append(out, notice{ID: "quota-full:" + p.Name, Severity: noticeError, Kind: "project",
					Title: p.Name + " is at its quota: uploads are refused", Detail: "Raise the quota, or shorten the retention.", Link: link})
			case share >= nearlyFull:
				out = append(out, notice{ID: "quota-near:" + p.Name, Severity: noticeWarn, Kind: "project",
					Title: fmt.Sprintf("%s is at %.0f%% of its quota", p.Name, 100*share), Detail: "Uploads stop at 100%.", Link: link})
			}
		}
		if len(p.Members) == 0 {
			out = append(out, notice{ID: "empty:" + p.Name, Severity: noticeInfo, Kind: "project",
				Title: p.Name + " has no members", Link: link})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return severityRank[out[i].Severity] < severityRank[out[j].Severity] })
	return out, a.withReadMarks(r.Context(), out)
}

// withReadMarks says which notices the admin has read, and forgets the marks
// of notices that are gone.
func (a *Admin) withReadMarks(ctx context.Context, out []notice) error {
	marks, err := a.st.NoticesRead(ctx)
	if err != nil {
		return err
	}
	current := make(map[string]bool, len(out))
	for i := range out {
		current[out[i].ID] = true
		if t, ok := marks[out[i].ID]; ok {
			t := t
			out[i].Read = &t
		}
	}
	return a.st.ForgetNoticesRead(ctx, current)
}

func (a *Admin) noticeList(w http.ResponseWriter, r *http.Request, _ access.Who) {
	out, err := a.notices(r, time.Now())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notices": out})
}

func (a *Admin) noticesRead(w http.ResponseWriter, r *http.Request, _ access.Who) {
	var in struct {
		IDs  []string `json:"ids"`
		Read *bool    `json:"read"`
	}
	if err := readJSON(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 500 {
		writeError(w, http.StatusBadRequest, string(access.BadValue), "Name between 1 and 500 notices.")
		return
	}
	if err := a.st.MarkNotices(r.Context(), in.IDs, in.Read == nil || *in.Read); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
