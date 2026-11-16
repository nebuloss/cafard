package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPushFlagsThatDoNotGoTogether(t *testing.T) {
	vars := map[string]string{"CAFARD_SERVER": "http://127.0.0.1:1", "CAFARD_TOKEN": "cfd_x"}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--stream", "s", "--serial", "COM5", "--file", "x"}, "not both"},
		{[]string{"--stream", "s", "--baud", "9600"}, "--baud goes with --serial"},
		{[]string{"--stream", "s", "--follow"}, "--follow goes with --file"},
		{[]string{"--stream", "s", "--file", "x", "--from-start"}, "--from-start goes with --follow"},
	} {
		_, _, err := runArgs(t, vars, append([]string{"push"}, c.args...)...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

func TestPushSendsAFile(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "gzip", http.StatusBadRequest)
			return
		}
		sc := bufio.NewScanner(zr)
		mu.Lock()
		defer mu.Unlock()
		for sc.Scan() {
			var l struct{ Text string }
			json.Unmarshal(sc.Bytes(), &l)
			lines = append(lines, l.Text)
		}
		w.Write([]byte(`{"accepted":1,"last_seq":1}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "putty.log")
	os.WriteFile(path, []byte("a\r\nb\rc\n"), 0o600)
	vars := map[string]string{"CAFARD_SERVER": srv.URL, "CAFARD_TOKEN": "cfd_x", "HOME": dir}
	if _, errOut, err := runArgs(t, vars, "push", "--stream", "bench", "--file", path); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(lines, ",") != "a,b,c" {
		t.Errorf("server got %q", lines)
	}
	// Nothing was spooled, so nothing was left in the cache directory.
	if _, err := os.Stat(filepath.Join(dir, ".cache", "cafard")); !os.IsNotExist(err) {
		t.Errorf("cache directory touched: %v", err)
	}
}
