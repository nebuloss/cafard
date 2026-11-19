package dto

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite internal/web/testdata/api/*.json")

const fixtureDir = "../web/testdata/api"

func ts(s string) Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return TimeOf(t)
}

func tsp(s string) *Time { t := ts(s); return &t }

func ptr[T any](v T) *T { return &v }

func lineEntry(seq int64, at, text string, sev int) Entry {
	return Entry{
		Seq:      seq,
		TS:       ts(at),
		Stream:   "serial:lab/dut1",
		Kind:     EntryLine,
		Severity: sev,
		Host:     "bench-pi",
		Text:     text,
	}
}

func imageAttachment() ImageAttachment {
	return ImageAttachment{
		SHA256:   "9f2c6a1e4b7d3f8a0c5e2b9d6a3f7c1e8b4d0a6f3c9e5b2d8a1f7c4e0b6d3a9f",
		Mime:     "image/jpeg",
		Width:    4032,
		Height:   3024,
		URL:      "/blob/9f2c6a1e4b7d3f8a0c5e2b9d6a3f7c1e8b4d0a6f3c9e5b2d8a1f7c4e0b6d3a9f",
		ThumbURL: "/blob/9f2c6a1e4b7d3f8a0c5e2b9d6a3f7c1e8b4d0a6f3c9e5b2d8a1f7c4e0b6d3a9f?v=thumb",
		Preview:  true,
	}
}

func logPreview() []Entry {
	return []Entry{
		lineEntry(48211, "2026-11-16T09:41:02.118734502Z", "[    2.315842] mmc0: new HS200 MMC card at address 0001", 6),
		lineEntry(48212, "2026-11-16T09:41:02.240117893Z", "[    2.402113] EXT4-fs (mmcblk0p2): mounted filesystem with ordered data mode", 6),
		lineEntry(48213, "2026-11-16T09:41:02.512904417Z", "[    2.689001] dwc3 fe800000.usb: failed to initialize core: -110", 3),
	}
}

func closedLog() LogAttachment {
	return LogAttachment{
		Stream:   "serial:lab/dut1",
		Streams:  []string{"serial:lab/dut1"},
		FromSeq:  48211,
		ToSeq:    ptr(int64(48390)),
		Title:    "boot with the new kernel",
		Lines:    180,
		FirstTS:  tsp("2026-11-16T09:41:02.118734502Z"),
		LastTS:   tsp("2026-11-16T09:41:09.873120044Z"),
		Snapshot: true,
		Preview:  logPreview(),
	}
}

func openLog() LogAttachment {
	return LogAttachment{
		Channel:     "dut1",
		Streams:     []string{"serial:lab/dut1", "syslog:lab/dut1"},
		FromSeq:     48391,
		ToSeq:       nil,
		Grep:        "usb|dwc3",
		MinSeverity: ptr(4),
		Title:       "USB errors from now on",
		Lines:       2,
		FirstTS:     tsp("2026-11-16T09:42:15.004417120Z"),
		LastTS:      tsp("2026-11-16T09:42:16.331952087Z"),
		Snapshot:    false,
		Expired:     false,
		Preview: []Entry{
			lineEntry(48402, "2026-11-16T09:42:15.004417120Z", "[   75.118230] dwc3 fe800000.usb: failed to initialize core: -110", 3),
			lineEntry(48405, "2026-11-16T09:42:16.331952087Z", "[   76.445871] usb usb1-port1: connect-debounce failed", 4),
		},
	}
}

func messageEntry() Entry {
	return Entry{
		Seq:       48395,
		TS:        ts("2026-11-16T09:42:30.551209874Z"),
		Stream:    "chan:lab/dut1",
		Kind:      EntryMessage,
		Severity:  6,
		Text:      "The new kernel boots but `dwc3` fails to init, see the log. Photo of the board attached. Patch at https://git.example.com/kernel/commit/4a1f",
		Meta:      json.RawMessage(`{"client":"claude-code"}`),
		Chat:      "chan:lab/dut1",
		Sender:    &Sender{Name: "claude-laptop", Kind: KindAgent},
		Recipient: RecipientHumans,
		ReplyTo:   48390,
		Segments: []Segment{
			{T: SegText, V: "The new kernel boots but "},
			{T: SegCode, V: "dwc3"},
			{T: SegText, V: " fails to init, see the log. Photo of the board attached. Patch at "},
			{T: SegLink, V: "https://git.example.com/kernel/commit/4a1f", Href: "https://git.example.com/kernel/commit/4a1f"},
		},
		Attachments: []Attachment{ImageOf(imageAttachment()), LogOf(closedLog())},
		SeenBy:      []string{"guillaume"},
	}
}

func questionEntry() Entry {
	return Entry{
		Seq:       48410,
		TS:        ts("2026-11-16T09:43:05.120004561Z"),
		Stream:    "chan:lab/dut1",
		Kind:      EntryMessage,
		Severity:  6,
		Text:      "Should I flash the board with the patched kernel?\n```sh\nflashtool --port /dev/ttyUSB0 write boot.img\n```",
		Chat:      "chan:lab/dut1",
		Sender:    &Sender{Name: "claude-laptop", Kind: KindAgent},
		Recipient: RecipientHumans,
		Segments: []Segment{
			{T: SegText, V: "Should I flash the board with the patched kernel?\n"},
			{T: SegCodeblock, V: "flashtool --port /dev/ttyUSB0 write boot.img", Lang: "sh"},
		},
		Question: &Question{
			Choices: []string{"yes", "no", "wait"},
			Open:    false,
			Answer:  &Answer{By: "guillaume", Choice: "yes", Seq: 48412},
		},
	}
}

func markerEntry() Entry {
	return Entry{
		Seq:      48390,
		TS:       ts("2026-11-16T09:41:00.000312998Z"),
		SrcTS:    tsp("2026-11-16T09:40:59.998001200Z"),
		Stream:   "serial:lab/dut1",
		Kind:     EntryMarker,
		Severity: 5,
		App:      "flashtool",
		Text:     "flashed linux-6.18-rc3 to dut1",
		Sender:   &Sender{Name: "codex-build", Kind: KindAgent},
		Chat:     "chan:lab/dut1",
		Event:    true,
	}
}

func channelChat() Chat {
	return Chat{
		ID:            "chan:lab/dut1",
		Kind:          ChatChannel,
		Project:       "lab",
		ProjectTitle:  "Lab bench",
		Name:          "dut1",
		Title:         "DUT 1 (RK3588 board)",
		Topic:         "Bring-up of the 6.18 kernel",
		Patterns:      []string{"serial:lab/dut1", "syslog:lab/dut1*"},
		Streams:       []StreamRef{{ID: "serial:lab/dut1", Status: StreamConnected}, {ID: "syslog:lab/dut1", Status: StreamIdle}},
		Surface:       SurfaceMarkers,
		Followed:      true,
		Pinned:        true,
		Source:        SourceRuntime,
		CreatedBy:     "guillaume",
		CanManage:     true,
		Unread:        3,
		OpenQuestions: 1,
		LastActivity:  tsp("2026-11-16T09:43:05.120004561Z"),
		Preview:       &ChatPreview{Seq: 48410, Sender: "claude-laptop", Text: "Should I flash the board with the patched kernel?", Event: false},
	}
}

func directChat() Chat {
	return Chat{
		ID:           "dm:codex-build",
		Kind:         ChatDirect,
		Name:         "codex-build",
		Peer:         &Principal{Name: "codex-build", Kind: KindAgent, LastSeen: tsp("2026-11-16T09:44:12.700300100Z")},
		Patterns:     []string{},
		Streams:      []StreamRef{},
		Surface:      SurfaceNone,
		Followed:     true,
		Source:       SourceRuntime,
		Unread:       1,
		LastActivity: tsp("2026-11-16T09:44:12.700300100Z"),
		Preview:      &ChatPreview{Seq: 48420, Sender: "codex-build", Text: "Build 812 is green, artifacts in /srv/builds/812.", Event: false},
	}
}

func token() Token {
	return Token{
		ID:        7,
		Label:     "laptop MCP",
		Principal: "claude-laptop",
		Kind:      KindAgent,
		Scopes:    []Scope{ScopeRead, ScopeMessage},
		CreatedAt: ts("2026-11-02T14:05:33.402118000Z"),
		LastUsed:  tsp("2026-11-16T09:42:30.551209874Z"),
	}
}

type fixture struct {
	name  string
	value any
}

func fixtures() []fixture {
	fs := []fixture{
		{"me", Me{Name: "guillaume", Kind: KindHuman, Scopes: []Scope{ScopeRead, ScopeMessage, ScopeSerialWrite, ScopeAdmin}, SessionID: 12, Theme: ThemeSystem}},
		{"principal", Principal{Name: "claude-laptop", Kind: KindAgent, LastSeen: tsp("2026-11-16T09:42:30.551209874Z")}},
		{"entry_line", lineEntry(48213, "2026-11-16T09:41:02.512904417Z", "[    2.689001] dwc3 fe800000.usb: failed to initialize core: -110", 3)},
		{"entry_marker", markerEntry()},
		{"entry_message", messageEntry()},
		{"entry_question", questionEntry()},
		{"attachment_image", ImageOf(ImageAttachment{
			SHA256:  "4e1b7c9a2d5f8e0b3a6c9f2e5d8b1a4c7f0e3d6b9a2c5f8e1b4d7a0c3f6e9b2d",
			Mime:    "image/heic",
			Width:   3024,
			Height:  4032,
			URL:     "/blob/4e1b7c9a2d5f8e0b3a6c9f2e5d8b1a4c7f0e3d6b9a2c5f8e1b4d7a0c3f6e9b2d",
			Preview: false,
		})},
		{"attachment_log", LogOf(openLog())},
		{"chat_channel", channelChat()},
		{"chat_direct", directChat()},
		{"stream", Stream{
			ID:           "serial:lab/dut1",
			Kind:         "serial",
			Project:      "lab",
			Sink:         "dut1",
			Transport:    "rfc2217",
			Labels:       map[string]string{"board": "rk3588", "bench": "desk-left"},
			Status:       StreamConnected,
			LastSeq:      48405,
			LastActivity: tsp("2026-11-16T09:42:16.331952087Z"),
			RatePerMin:   42.6,
			Dropped:      0,
			Claim:        &Claim{Principal: "claude-laptop", Note: "flashing the 6.18 kernel", ExpiresAt: ts("2026-11-16T10:12:00Z")},
			LineState:    &LineState{DTR: ptr(true), RTS: ptr(false), CTS: ptr(true), DCD: ptr(false)},
			LastLine:     "[   76.445871] usb usb1-port1: connect-debounce failed",
		}},
		{"overview", Overview{
			Questions: []Entry{questionEntry()},
			Direct:    []Chat{directChat()},
			Recent:    []Entry{messageEntry(), markerEntry()},
		}},
		{"counts", Counts{
			OpenQuestions: 1,
			UnreadDirect:  1,
			SourcesDown:   0,
			Chats: []ChatCount{
				{ID: "chan:lab/dut1", Unread: 3, OpenQuestions: 1},
				{ID: "dm:codex-build", Unread: 1, OpenQuestions: 0},
			},
		}},
		{"channel_create", ChannelCreate{Name: "dut2", Project: "lab", Title: "DUT 2 (i.MX8M)", Topic: "U-Boot work", Patterns: []string{"serial:lab/dut2", "netconsole:lab/dut2"}, Surface: SurfaceErrors}},
		{"channel_update", ChannelUpdate{
			Rename:         "dut2-imx",
			Topic:          ptr("U-Boot 2026.10 bring-up"),
			AddPatterns:    []string{"syslog:lab/dut2"},
			RemovePatterns: []string{"netconsole:lab/dut2"},
			Surface:        SurfaceMarkers,
			Archived:       ptr(false),
		}},
		{"token", token()},
		{"token_created", TokenCreated{
			Token:  token(),
			Secret: "cfd_7_Qm9vdHN0cmFwLXNlY3JldC1leGFtcGxlLW5vdC1yZWFs",
			Snippets: []Snippet{
				{Label: "Claude Code", Language: LangShell, Text: "claude mcp add --transport http cafard https://cafard.lan/mcp --header \"Authorization: Bearer cfd_7_Qm9vdHN0cmFwLXNlY3JldC1leGFtcGxlLW5vdC1yZWFs\""},
				{Label: "Codex", Language: LangTOML, Text: "[mcp_servers.cafard]\nurl = \"https://cafard.lan/mcp\"\nbearer_token_env_var = \"CAFARD_TOKEN\""},
				{Label: "Environment", Language: LangEnv, Text: "CAFARD_URL=https://cafard.lan\nCAFARD_TOKEN=cfd_7_Qm9vdHN0cmFwLXNlY3JldC1leGFtcGxlLW5vdC1yZWFs"},
			},
		}},
		{"session", Session{
			ID:        12,
			Label:     "Firefox on Linux",
			Principal: "guillaume",
			UserAgent: "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0",
			CreatedAt: ts("2026-11-10T08:15:42.993001000Z"),
			ExpiresAt: ts("2026-12-10T08:15:42.993001000Z"),
			Current:   true,
		}},
		{"pairing", Pairing{
			ID:        3,
			URL:       "https://cafard.lan/pair/Zk3pQ8wX2r",
			QRURL:     "/api/pairings/3/qr.png",
			ExpiresAt: ts("2026-11-16T09:55:00Z"),
			Principal: "guillaume",
			NewName:   "guillaume-phone",
			Grants:    []string{"lab=read,message", "rig:dut1=read"},
		}},
		{"share", Share{
			ID:        "shr_b81c40e2",
			Text:      "Screen of the bench scope after the reset",
			Files:     []ImageAttachment{imageAttachment()},
			ExpiresAt: ts("2026-11-16T10:42:30Z"),
		}},
		{"page_entry", Page[Entry]{
			Items:         []Entry{markerEntry(), messageEntry(), questionEntry()},
			HasMore:       true,
			NextSeq:       48411,
			NextBeforeSeq: 48390,
			FocusSeq:      48395,
		}},
		{"seen", Seen{Seq: 48395, By: "guillaume"}},
		{"answered", Answered{Seq: 48410, Answer: Answer{By: "guillaume", Choice: "yes", Seq: 48412}}},
		{"reset", Reset{Reason: "lagged"}},
		{"send_request", SendRequest{
			Text:    "Kernel 6.18-rc3 boots, USB still fails. Log and photo attached.",
			To:      RecipientHumans,
			Channel: "dut1",
			ReplyTo: 48390,
			Images:  []ImageUpload{{Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", Mime: "image/png"}},
			Logs: []LogRequest{
				{Stream: "serial:lab/dut1", FromSeq: 48211, ToSeq: ptr(int64(48390)), Title: "boot with the new kernel"},
				{Channel: "dut1", FromSeq: 48391, Grep: "usb|dwc3", MinSeverity: ptr(4)},
			},
		}},
		{"log_accepted", LogAccepted{Accepted: 1000, LastSeq: 49211, Dropped: 12}},
		{"stream_list", StreamList{Streams: []Stream{{ID: "syslog:lab/bench-pi", Kind: "syslog", Transport: "udp", Project: "lab", Sink: "bench-pi",
			Status: StreamIdle, LastSeq: 48120, RatePerMin: 0.5}}, Unrouted: map[string]int64{"syslog": 31, "http": 2}}},
		{"project", Project{
			Name:      "lab",
			Title:     "Lab bench",
			CreatedAt: ts("2026-11-16T08:00:00Z"),
			Scopes:    []Scope{ScopeRead, ScopeMessage},
			Channels:  []string{"chan:lab/dut1", "chan:lab/general"},
			Sinks:     []string{"bench-pi", "dut1"},
			Retention: ProjectRetention{LinesMaxAgeS: 1209600, LinesMaxBytes: 2e9, MessagesMaxAgeS: 7776000, BlobsMaxBytes: 5e9},
		}},
		{"token_create", TokenCreate{Principal: "claude-laptop", Kind: KindAgent, Label: "laptop MCP", Scopes: []Scope{ScopeRead, ScopeMessage}}},
		{"pairing_create", PairingCreate{NewName: "guillaume-phone", Scopes: []Scope{ScopeRead, ScopeMessage, ScopeIngest}}},
		{"mark_create", MarkCreate{Stream: "serial:lab/dut1", Label: "test 42 starts"}},
		{"mark_created", MarkCreated{Seq: 48230}},
		{"chat_prefs", ChatPrefs{Pinned: ptr(true), Muted: ptr(false)}},
		{"chat_read", ChatRead{UpToSeq: 48420}},
		{"ack_request", AckRequest{Seqs: []int64{48395, 48410}}},
		{"acked", Acked{Acked: 2}},
	}
	for _, code := range Codes {
		fs = append(fs, fixture{"error_" + code, Error{Error: errorExample(code)}})
	}
	return fs
}

func errorExample(code string) ErrorBody {
	b := ErrorBody{Code: code}
	switch code {
	case CodeBadRequest:
		b.Message = "from_seq must be a positive integer."
	case CodeInvalidPattern:
		b.Message = "The pattern \"serial:[dut\" is not a valid glob: missing ']'."
	case CodeUnknownChannel:
		b.Message = "There is no channel \"dut3\" you can read. Channels you can read: lab/dut1, lab/dut2, lab/general."
	case CodeTooLarge:
		b.Message = "The message text is 40 KiB; the limit is 32 KiB."
	case CodeUnauthenticated:
		b.Message = "Sign in or send a valid token."
	case CodeForbidden:
		b.Message = "This token lacks the serial_write scope."
	case CodeCrossOrigin:
		b.Message = "This request came from another site and was refused."
	case CodeNotFound:
		b.Message = "Message 48500 does not exist."
	case CodeConflict:
		b.Message = "serial:lab/dut1 is claimed by claude-laptop until 10:12 UTC."
	case CodeRateLimited:
		b.Message = "You sent 30 messages in the last minute; wait 12 s."
		b.RetryAfterS = 12
	case CodeBusy:
		b.Message = "The server is busy writing; retry in 2 s."
		b.RetryAfterS = 2
	}
	return b
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return append(b, '\n')
}

func TestFixtures(t *testing.T) {
	fs := fixtures()
	if *update {
		if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]bool{}
	for _, f := range fs {
		file := f.name + ".json"
		if want[file] {
			t.Fatalf("two fixtures named %s", file)
		}
		want[file] = true
		path := filepath.Join(fixtureDir, file)
		got := encode(t, f.value)
		if *update {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		disk, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v (run go test ./internal/dto -update)", path, err)
			continue
		}
		if !bytes.Equal(disk, got) {
			t.Errorf("%s differs from the Go value (run go test ./internal/dto -update)", path)
		}
	}
	files, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for _, p := range files {
		if !want[filepath.Base(p)] {
			stale = append(stale, filepath.Base(p))
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		if *update {
			for _, s := range stale {
				if err := os.Remove(filepath.Join(fixtureDir, s)); err != nil {
					t.Fatal(err)
				}
			}
			return
		}
		t.Errorf("no fixture produces %s", strings.Join(stale, ", "))
	}
}

func TestFixturesRoundTrip(t *testing.T) {
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			disk, err := os.ReadFile(filepath.Join(fixtureDir, f.name+".json"))
			if err != nil {
				t.Skipf("no file yet: %v", err)
			}
			v := reflect.New(reflect.TypeOf(f.value))
			if err := json.Unmarshal(disk, v.Interface()); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := encode(t, v.Elem().Interface()); !bytes.Equal(got, disk) {
				t.Errorf("re-marshalled JSON differs:\n%s\nwant:\n%s", got, disk)
			}
		})
	}
}
