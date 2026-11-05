package cli

import (
	"flag"
	"strings"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	as := fs.String("as", "", "")
	j := fs.Bool("json", false, "")
	err := parseInterspersed(fs, []string{"@bob", "--as", "alice", "hello", "--json", "--", "--not-a-flag"})
	if err != nil {
		t.Fatal(err)
	}
	if *as != "alice" || !*j {
		t.Fatalf("flags: as=%q json=%v", *as, *j)
	}
	if got := strings.Join(fs.Args(), "|"); got != "@bob|hello|--not-a-flag" {
		t.Fatalf("args = %q", got)
	}
}
