package messaging

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestChannelsConfigParsesAndRefusesUnknownKeys(t *testing.T) {
	var root struct {
		Channels Channels `yaml:"channels"`
	}
	good := "channels:\n  - name: general\n    topic: misc\n  - name: dut1\n    streams: [serial:dut1, \"syslog:dut1*\"]\n    surface: errors\n"
	if err := yaml.Unmarshal([]byte(good), &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Channels.List) != 2 || root.Channels.List[1].Surface != "errors" || root.Channels.Validate() != nil {
		t.Errorf("parsed = %+v", root.Channels)
	}
	out, err := yaml.Marshal(root)
	if err != nil || !strings.Contains(string(out), "- name: dut1") {
		t.Errorf("marshal = %s %v", out, err)
	}
	if err := yaml.Unmarshal([]byte("channels:\n  - name: dut1\n    stream: [x:y]\n"), &root); err == nil {
		t.Error("unknown key accepted")
	}
	bad := Channels{List: []ChannelConfig{{Name: "dut1", Streams: []string{"serial:[x"}}}}
	if bad.Validate() == nil {
		t.Error("bad glob accepted")
	}
}
