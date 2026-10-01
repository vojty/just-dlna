package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "just-dlna.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyConfigFile(t *testing.T) {
	var name, ips, path, charset string
	var port int
	var prefetch bool
	fset := flag.NewFlagSet("test", flag.ContinueOnError)
	fset.StringVar(&name, "name", "", "")
	fset.StringVar(&ips, "allowed-ips", "", "")
	fset.StringVar(&path, "path", ".", "")
	fset.StringVar(&charset, "sub-charset", "", "")
	fset.IntVar(&port, "http-port", 1338, "")
	fset.BoolVar(&prefetch, "prefetch-subs", true, "")
	keys := map[string]string{
		"name":          "TEST_FRIENDLY_NAME",
		"allowed-ips":   "TEST_ALLOWED_IPS",
		"path":          "TEST_MEDIA_PATH",
		"sub-charset":   "TEST_SUB_CHARSET",
		"http-port":     "TEST_HTTP_PORT",
		"prefetch-subs": "TEST_PREFETCH_SUBS",
	}
	if err := fset.Parse([]string{"-name", "from flag"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SUB_CHARSET", "from env")
	charset = "from env" // as env() would have set the default

	cfg := writeConfig(t, `
name: from file
allowed-ips: [192.168.1.0/24, "fd00::/8"]
path: ~/Videos
sub-charset: from file
http-port: 8080
prefetch-subs: false
`)
	if err := applyConfigFile(fset, cfg, keys); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	for _, tc := range []struct{ field, got, want string }{
		{"name", name, "from flag"},
		{"sub-charset", charset, "from env"},
		{"allowed-ips", ips, "192.168.1.0/24,fd00::/8"},
		{"path", path, filepath.Join(home, "Videos")},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}
	if port != 8080 {
		t.Errorf("http-port = %d, want 8080", port)
	}
	if prefetch {
		t.Error("prefetch-subs = true, want false")
	}
}

func TestApplyConfigFileErrors(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{"nmae: x\n", `unknown setting "nmae"`},
		{"http-port: abc\n", "http-port"},
		{"name: {a: b}\n", "nested mappings"},
		{"name: [\n", "yaml"},
	} {
		fset := flag.NewFlagSet("test", flag.ContinueOnError)
		fset.String("name", "", "")
		fset.Int("http-port", 0, "")
		keys := map[string]string{"name": "TEST_FRIENDLY_NAME", "http-port": "TEST_HTTP_PORT"}
		err := applyConfigFile(fset, writeConfig(t, tc.content), keys)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: error %v, want containing %q", tc.content, err, tc.want)
		}
	}
}

func TestApplyConfigFileEmpty(t *testing.T) {
	fset := flag.NewFlagSet("test", flag.ContinueOnError)
	if err := applyConfigFile(fset, writeConfig(t, "# nothing\n"), nil); err != nil {
		t.Fatal(err)
	}
}
