package client

import (
	"flag"
	"testing"
)

func TestPartName(t *testing.T) {
	for _, c := range []struct {
		name string
		part int
		want string
	}{{"build.log", 1, "build.log"}, {"build.log", 2, "build.2.log"}, {"console", 3, "console.3"}} {
		if got := partName(c.name, c.part); got != c.want {
			t.Errorf("partName(%q, %d) = %q", c.name, c.part, got)
		}
	}
}

// Flags anywhere among the arguments; a command's own after "--".
func TestFlags(t *testing.T) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	name := fs.String("name", "run.log", "")
	pos, err := flags(fs, []string{"be98/rc3", "-name", "build.log", "--", "sh", "-c", "exit 3"})
	if err != nil || *name != "build.log" || len(pos) != 5 || pos[1] != "--" || pos[3] != "-c" {
		t.Errorf("%q %v %q", pos, err, *name)
	}
}
