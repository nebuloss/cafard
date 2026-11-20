package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The chat JSON API (CONTRACTS.md 2.3): the list, the overview,
// timelines, prefs, read state, channels and staged shares.

// chatsAPI holds what the chat routes need on top of API.
type chatsAPI struct {
	*API
	svc    *messaging.Service
	shares *view.Shares
}

// MountChats registers the chat routes. They need the messaging service
// itself, not the deps interface.
func (a *API) MountChats(mux *http.ServeMux, svc *messaging.Service, shares *view.Shares) {
	c := &chatsAPI{API: a, svc: svc, shares: shares}
	mux.Handle("GET /api/chats", a.Read(auth.ScopeRead, c.list))
	mux.Handle("GET /api/overview", a.Read(auth.ScopeRead, c.overview))
	mux.Handle("GET /api/chats/{chat}/timeline", a.Read(auth.ScopeRead, c.timeline))
	mux.Handle("PUT /api/chats/{chat}/prefs", a.Write(auth.ScopeMessage, c.prefs))
	mux.Handle("POST /api/chats/{chat}/read", a.Write(auth.ScopeMessage, c.read))
	mux.Handle("POST /api/acks", a.Write(auth.ScopeMessage, c.acks))
	mux.Handle("POST /api/channels", a.Write(auth.ScopeMessage, c.createChannel))
	mux.Handle("PATCH /api/channels/{name}", a.Write(auth.ScopeMessage, c.updateChannel))
	mux.Handle("DELETE /api/channels/{name}", a.Write(auth.ScopeMessage, c.deleteChannel))
	mux.Handle("GET /api/channels/{name}/members", a.Read(auth.ScopeRead, c.members))
	mux.Handle("GET /api/shares/{id}", a.Read(auth.ScopeMessage, c.share))
	mux.Handle("GET /api/galleries/{name}", a.Read(auth.ScopeRead, c.gallery))
}

func flag(r *http.Request, name string) bool {
	v := r.URL.Query().Get(name)
	return v == "1" || v == "true"
}

func (c *chatsAPI) list(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	chats, err := c.svc.Chats(r.Context(), p, messaging.ChatsQuery{All: flag(r, "all"), Archived: flag(r, "archived"),
		Project: r.URL.Query().Get("project")})
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string][]dto.Chat{"chats": nonNil(chats)})
}

func (c *chatsAPI) overview(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	o, err := c.svc.Overview(r.Context(), p)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, o)
}

// chatStream resolves the {chat} path value to a stream p may see.
func (c *chatsAPI) chatStream(r *http.Request, p auth.Principal) (string, error) {
	stream, err := messaging.StreamOfChat(p.Name, r.PathValue("chat"))
	if err != nil {
		return "", err
	}
	if _, err := c.svc.ChatOfStream(r.Context(), p, stream); err != nil {
		var apiErr *dto.APIError
		if errors.As(err, &apiErr) && apiErr.Code == dto.CodeUnknownChannel {
			return "", dto.Errorf(dto.CodeNotFound, "%s", apiErr.Message)
		}
		return "", err
	}
	return stream, nil
}

// TimelineQuery reads before_seq, since_seq and limit.
func TimelineQuery(r *http.Request) (messaging.TimelineQuery, *dto.APIError) {
	var q messaging.TimelineQuery
	for name, dst := range map[string]*int64{"before_seq": &q.BeforeSeq, "since_seq": &q.SinceSeq} {
		if v := r.URL.Query().Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return q, dto.Errorf(dto.CodeBadRequest, "%s must be a positive integer.", name)
			}
			*dst = n
		}
	}
	limit, apiErr := intParam(r, "limit", 50, 500)
	if apiErr != nil {
		return q, apiErr
	}
	q.Limit = limit
	return q, nil
}

func (c *chatsAPI) timeline(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ctx := r.Context()
	stream, err := c.chatStream(r, p)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	q, apiErr := TimelineQuery(r)
	if apiErr != nil {
		dto.WriteError(w, apiErr)
		return
	}
	f := r.URL.Query().Get("f")
	if !messaging.ValidFilter(f) {
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "f is all, questions, events, mine or images, not %q.", f))
		return
	}
	if q.SinceSeq == 0 && q.BeforeSeq == 0 {
		// No cursor: the latest page, which is what a chat opens on.
		last, err := c.d.Store.LastSeq(ctx)
		if err != nil {
			c.Fail(w, r, err)
			return
		}
		q.BeforeSeq = last + 1
	}
	page, err := c.svc.FilteredTimeline(ctx, p, stream, q, f)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	out, err := c.svc.TimelineDTO(ctx, p, page)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	out.Items = nonNil(out.Items)
	dto.WriteJSON(w, http.StatusOK, out)
}

func (c *chatsAPI) prefs(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ctx := r.Context()
	stream, err := c.chatStream(r, p)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	var req dto.ChatPrefs
	if !Decode(w, r, &req) {
		return
	}
	if req.Followed != nil {
		name, ok := strings.CutPrefix(stream, "chan:")
		if !ok {
			dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "A direct conversation is always followed; mute it instead."))
			return
		}
		if _, err := c.svc.SetFollow(ctx, p, name, *req.Followed); err != nil {
			c.Fail(w, r, err)
			return
		}
	}
	if req.Pinned != nil || req.Muted != nil {
		if err := c.svc.SetPrefs(ctx, p, stream, req.Pinned, req.Muted); err != nil {
			c.Fail(w, r, err)
			return
		}
	}
	chat, err := c.svc.ChatOfStream(ctx, p, stream)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, chat)
}

func (c *chatsAPI) read(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	stream, err := c.chatStream(r, p)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	var req dto.ChatRead
	if !Decode(w, r, &req) {
		return
	}
	if req.UpToSeq < 1 {
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "up_to_seq must be a message seq."))
		return
	}
	n, err := c.svc.AckUpTo(r.Context(), p, stream, req.UpToSeq)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.Acked{Acked: n})
}

const maxAcks = 500

func (c *chatsAPI) acks(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var req dto.AckRequest
	if !Decode(w, r, &req) {
		return
	}
	if len(req.Seqs) > maxAcks {
		dto.WriteError(w, dto.Errorf(dto.CodeTooLarge, "Ack at most %d messages at once.", maxAcks))
		return
	}
	n, err := c.svc.Ack(r.Context(), p, req.Seqs)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.Acked{Acked: n})
}

func (c *chatsAPI) createChannel(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var req dto.ChannelCreate
	if !Decode(w, r, &req) {
		return
	}
	ch, err := c.svc.CreateChannel(r.Context(), p, req)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	chat, err := c.svc.ChannelChat(r.Context(), p, ch)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, chat)
}

func (c *chatsAPI) updateChannel(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var req dto.ChannelUpdate
	if !Decode(w, r, &req) {
		return
	}
	ch, err := c.svc.UpdateChannel(r.Context(), p, r.PathValue("name"), req)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	chat, err := c.svc.ChannelChat(r.Context(), p, ch)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, chat)
}

func (c *chatsAPI) deleteChannel(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	name := r.PathValue("name")
	if confirm := r.URL.Query().Get("confirm"); confirm != strings.TrimPrefix(name, "chan:") {
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "Deleting #%s removes its messages; confirm with ?confirm=%s.", name, name))
		return
	}
	if err := c.svc.DeleteChannel(r.Context(), p, name); err != nil {
		c.Fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// members is the members panel of a channel: who can read it, among the
// principals the caller may know about.
func (c *chatsAPI) members(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	list, err := c.svc.ChannelMembers(r.Context(), p, strings.TrimPrefix(r.PathValue("name"), "chan:"), c.Presence.Online)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.MemberList{Members: list})
}

func (c *chatsAPI) share(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	sh, ok := c.shares.Get(p.Name, r.PathValue("id"))
	if !ok {
		dto.WriteError(w, dto.Errorf(dto.CodeNotFound, "The shared files are gone (they wait 1 hour, and a restart loses them). Share them again."))
		return
	}
	dto.WriteJSON(w, http.StatusOK, sh.DTO())
}

// gallery is a page of a gallery's items and its sections, for the
// gallery page's paging and the composer's gallery picker.
func (c *chatsAPI) gallery(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	q := r.URL.Query()
	ch, err := c.svc.Gallery(r.Context(), p, r.PathValue("name"))
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	var section *string
	if q.Has("section") {
		sec := q.Get("section")
		section = &sec
	}
	before, _ := strconv.ParseInt(q.Get("before_seq"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	page, err := c.svc.GalleryPage(r.Context(), ch, section, before, limit)
	if err != nil {
		c.Fail(w, r, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, page)
}
