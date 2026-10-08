package shell

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{`ls -l /t/bucket`, []string{"ls", "-l", "/t/bucket"}},
		{`cp "a file.txt" dest/`, []string{"cp", "a file.txt", "dest/"}},
		{`echo 'single quoted'`, []string{"echo", "single quoted"}},
		{`echo a\ b`, []string{"echo", "a b"}},
		{`put "" file`, []string{"put", "", "file"}},
		{`   `, nil},
	}
	for _, tt := range tests {
		got, err := tokenize(tt.in)
		if err != nil {
			t.Fatalf("tokenize(%q) error: %v", tt.in, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("tokenize(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
	}

	for _, bad := range []string{`"unterminated`, `'unterminated`, `trailing\`} {
		if _, err := tokenize(bad); err == nil {
			t.Fatalf("tokenize(%q) expected error", bad)
		}
	}
}

func TestParseArgs(t *testing.T) {
	defs := []flagDef{
		{Names: []string{"r", "recursive"}},
		{Names: []string{"f", "force"}},
		{Names: []string{"parallel", "p"}, Value: true},
	}

	pa, err := parseArgs([]string{"-rf", "a", "-p", "8", "--parallel=4", "b"}, defs)
	if err != nil {
		t.Fatal(err)
	}
	if !pa.Has("recursive") || !pa.Has("force") {
		t.Fatalf("clustered flags not parsed: %+v", pa.flags)
	}
	if got := pa.Get("parallel"); got != "4" {
		t.Fatalf("parallel = %q, want 4 (last wins)", got)
	}
	if got := pa.Positional(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("positional = %v", got)
	}

	if _, err := parseArgs([]string{"--nope"}, defs); err == nil {
		t.Fatal("expected unknown flag error")
	}
	if _, err := parseArgs([]string{"-p"}, defs); err == nil {
		t.Fatal("expected missing value error")
	}
}

func TestResolvePath(t *testing.T) {
	root := Path{}
	alias := Path{Alias: "t"}
	bucket := Path{Alias: "t", Bucket: "buck"}
	deep := Path{Alias: "t", Bucket: "buck", Key: "a/b"}

	tests := []struct {
		cwd  Path
		arg  string
		want Path
	}{
		{root, "/", root},
		{root, "/t", alias},
		{root, "t/buck", bucket},
		{root, "t", Path{Alias: "t"}},
		{alias, "buck/a/b", deep},
		{bucket, "a/b", deep},
		{deep, "..", Path{Alias: "t", Bucket: "buck", Key: "a"}},
		{deep, "../..", bucket},
		{deep, "..", Path{Alias: "t", Bucket: "buck", Key: "a"}},
		{deep, "../../..", alias},
		{deep, "../../../..", root},
		{deep, "~", root},
		{deep, "/t/buck/c", Path{Alias: "t", Bucket: "buck", Key: "c"}},
	}
	for _, tt := range tests {
		if got := resolvePath(tt.cwd, tt.arg); got != tt.want {
			t.Fatalf("resolvePath(%v, %q) = %v, want %v", tt.cwd, tt.arg, got, tt.want)
		}
	}
}
