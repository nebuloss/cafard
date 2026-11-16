// Package api is the JSON API under /api/ (except /api/log and
// /api/events). This file and blob.go are track A's: messages, their
// attachments and blobs.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/textclean"
)

const (
	maxFieldBytes = 64 << 10
	maxAPILines   = 1000
)

// RegisterMessages wires messaging: its MCP tools and janitor tasks, and
// the message and blob routes. start runs its background loops.
func RegisterMessages(mux *http.ServeMux, m *mcpserver.Server, d deps.Deps, svc *messaging.Service) (func(context.Context) error, error) {
	svc.RegisterTools(m)
	if d.Janitor != nil {
		for _, t := range svc.JanitorTasks() {
			d.Janitor.Add(t)
		}
	}
	h := &messagesAPI{d: d, svc: svc}
	// Bearer only until browser sessions land (track D adds session auth
	// with the same-origin check; the handler takes its principal from the
	// context either way).
	mux.Handle("POST /api/messages", auth.Require(auth.BearerOnly{Next: d.Auth}, auth.ScopeMessage, http.HandlerFunc(h.post)))
	mux.Handle("GET /api/messages/{seq}", auth.Require(d.Auth, auth.ScopeRead, http.HandlerFunc(h.get)))
	mux.Handle("GET /api/messages/{seq}/attachments/{index}", auth.Require(d.Auth, auth.ScopeRead, http.HandlerFunc(h.attachment)))
	mux.Handle("GET /blob/{sha}", auth.Require(d.Auth, auth.ScopeRead, http.HandlerFunc(h.blob)))
	return svc.Start, nil
}

type messagesAPI struct {
	d   deps.Deps
	svc *messaging.Service
}

// WantsJSON reports whether a form route should answer JSON rather than
// a 303 (CONTRACTS section 3.4).
func WantsJSON(r *http.Request) bool {
	return r.Header.Get("X-Cafard") == "1" || strings.Contains(r.Header.Get("Accept"), "application/json")
}

// ChatPath is the page of a chat id ("chan:x" or "dm:peer").
func ChatPath(chat string) string {
	if name, ok := strings.CutPrefix(chat, "chan:"); ok {
		return "/c/" + url.PathEscape(name)
	}
	if peer, ok := strings.CutPrefix(chat, "dm:"); ok {
		return "/dm/" + url.PathEscape(peer)
	}
	return "/"
}

func asAPIError(err error) *dto.APIError {
	var apiErr *dto.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return dto.Errorf(dto.CodeTooLarge, "The upload is larger than the server accepts.")
	}
	return nil
}

func (h *messagesAPI) fail(w http.ResponseWriter, r *http.Request, err error, back string) {
	apiErr := asAPIError(err)
	if apiErr == nil {
		if r.Context().Err() == nil {
			h.d.Log.Error("message request failed", "path", r.URL.Path, "err", err)
		}
		apiErr = dto.Errorf("internal", "Something went wrong on the server.")
	}
	if WantsJSON(r) || r.Method != http.MethodPost {
		dto.WriteError(w, apiErr)
		return
	}
	http.Redirect(w, r, back+"?error="+url.QueryEscape(apiErr.Code), http.StatusSeeOther)
}

// form is what POST /api/messages carries.
type form struct {
	chat  string
	in    messaging.SendInput
	files int
}

func (f *form) set(name, value string) error {
	switch name {
	case "chat":
		f.chat = value
	case "channel":
		f.in.Channel = value
	case "to":
		f.in.To = value
	case "text":
		f.in.Text = value
	case "choice":
		f.in.Choice = value
	case "staged":
		f.in.Staged = value
	case "reply_to":
		if value == "" {
			return nil
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 {
			return dto.Errorf(dto.CodeBadRequest, "reply_to must be a message seq.")
		}
		f.in.ReplyTo = n
	case "logs":
		if strings.TrimSpace(value) == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(value), &f.in.Logs); err != nil {
			return dto.Errorf(dto.CodeBadRequest, "logs must be a JSON array of {stream or channel, from_seq, to_seq, grep, min_severity, title}.")
		}
	}
	return nil
}

func (h *messagesAPI) post(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	img := h.svc.Blobs().Config()
	r.Body = http.MaxBytesReader(w, r.Body, int64(img.MaxFiles)*img.MaxFileBytes()+4<<20)
	var f form
	back := "/"
	if err := h.readForm(r, &f); err != nil {
		h.fail(w, r, err, back)
		return
	}
	if f.chat != "" {
		back = ChatPath(f.chat)
		stream, err := messaging.StreamOfChat(p.Name, f.chat)
		if err != nil {
			h.fail(w, r, err, back)
			return
		}
		if a, b, ok := messaging.DMParties(stream); ok {
			f.in.To, f.in.Channel = a, ""
			if a == p.Name {
				f.in.To = b
			}
		} else {
			f.in.To, f.in.Channel = "", strings.TrimPrefix(stream, "chan:")
		}
	}
	e, err := h.svc.SendMessage(r.Context(), p, f.in)
	if err != nil {
		h.fail(w, r, err, back)
		return
	}
	out, err := h.svc.EntryDTO(r.Context(), p, e)
	if err != nil {
		h.fail(w, r, err, back)
		return
	}
	if !WantsJSON(r) {
		http.Redirect(w, r, ChatPath(out.Chat), http.StatusSeeOther)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, out)
}

// readForm reads a multipart body part by part, storing files as they
// come, or a plain urlencoded form.
func (h *messagesAPI) readForm(r *http.Request, f *form) error {
	ctype, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ctype != "multipart/form-data" {
		if err := r.ParseForm(); err != nil {
			return dto.Errorf(dto.CodeBadRequest, "The form could not be read.")
		}
		for name, vs := range r.PostForm {
			if len(vs) > 0 {
				if err := f.set(name, vs[0]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return dto.Errorf(dto.CodeBadRequest, "The multipart body could not be read.")
	}
	img := h.svc.Blobs().Config()
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if apiErr := asAPIError(err); apiErr != nil {
				return apiErr
			}
			return dto.Errorf(dto.CodeBadRequest, "The multipart body could not be read.")
		}
		if err := h.readPart(r.Context(), part, f, img.MaxFiles); err != nil {
			part.Close()
			return err
		}
		part.Close()
	}
}

func (h *messagesAPI) readPart(ctx context.Context, part *multipart.Part, f *form, maxFiles int) error {
	name := part.FormName()
	if name == "files[]" || name == "files" || part.FileName() != "" {
		if part.FileName() == "" && name != "files[]" && name != "files" {
			return nil
		}
		f.files++
		if f.files > maxFiles {
			return dto.Errorf(dto.CodeTooLarge, "A message carries at most %d files.", maxFiles)
		}
		// Files are images: their type is sniffed, whatever the browser
		// declared (often application/octet-stream).
		b, err := h.svc.Blobs().Put(ctx, part, "")
		if err != nil {
			if apiErr := asAPIError(err); apiErr != nil {
				return dto.Errorf(apiErr.Code, "%s: %s", safeName(part.FileName()), apiErr.Message)
			}
			return err
		}
		f.in.Blobs = append(f.in.Blobs, b)
		return nil
	}
	limit := int64(maxFieldBytes)
	if name == "text" {
		limit = messaging.MaxTextBytes + 1
	}
	data, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil {
		if apiErr := asAPIError(err); apiErr != nil {
			return apiErr
		}
		return dto.Errorf(dto.CodeBadRequest, "The multipart body could not be read.")
	}
	if int64(len(data)) > limit {
		if name == "text" {
			return dto.Errorf(dto.CodeTooLarge, "The message text is over %d KiB, the limit.", messaging.MaxTextBytes>>10)
		}
		return dto.Errorf(dto.CodeTooLarge, "The field %s is too long.", name)
	}
	return f.set(name, string(data))
}

// safeName is a file name fit to quote back in an error.
func safeName(name string) string {
	name = textclean.StripANSI(name)
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		return "A file"
	}
	return strconv.Quote(name)
}

func pathSeq(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil && n > 0
}

func (h *messagesAPI) get(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	seq, ok := pathSeq(r, "seq")
	if !ok {
		dto.WriteError(w, dto.Errorf(dto.CodeNotFound, "There is no such message."))
		return
	}
	e, err := h.svc.Message(r.Context(), p, seq)
	if err != nil {
		h.fail(w, r, err, "/")
		return
	}
	out, err := h.svc.EntryDTO(r.Context(), p, e)
	if err != nil {
		h.fail(w, r, err, "/")
		return
	}
	dto.WriteJSON(w, http.StatusOK, out)
}

func (h *messagesAPI) attachment(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	seq, ok := pathSeq(r, "seq")
	index, err := strconv.Atoi(r.PathValue("index"))
	if !ok || err != nil {
		dto.WriteError(w, dto.Errorf(dto.CodeNotFound, "There is no such attachment."))
		return
	}
	q := r.URL.Query()
	var since int64
	if v := q.Get("since_seq"); v != "" {
		if since, err = strconv.ParseInt(v, 10, 64); err != nil {
			dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "since_seq must be a seq."))
			return
		}
	}
	limit := 200
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 {
			dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "limit must be a positive number."))
			return
		}
	}
	page, _, err := h.svc.AttachmentLines(r.Context(), p, seq, index, since, min(limit, maxAPILines))
	if err != nil {
		h.fail(w, r, err, "/")
		return
	}
	out := dto.Page[dto.Entry]{HasMore: page.HasMore, NextSeq: page.NextSeq}
	for _, e := range page.Entries {
		out.Items = append(out.Items, messaging.BaseDTO(e))
	}
	dto.WriteJSON(w, http.StatusOK, out)
}
