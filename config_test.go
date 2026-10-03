package main

import (
	"errors"
	"flag"
	"net"
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
	fset.StringVar(&ips, "tags", "", "")
	fset.StringVar(&path, "path", ".", "")
	fset.StringVar(&charset, "sub-charset", "", "")
	fset.IntVar(&port, "http-port", 1338, "")
	fset.BoolVar(&prefetch, "prefetch-subs", true, "")
	keys := map[string]string{
		"name":          "TEST_FRIENDLY_NAME",
		"tags":          "TEST_TAGS",
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
tags: [192.168.1.0/24, "fd00::/8"]
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
		{"tags", ips, "192.168.1.0/24,fd00::/8"},
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
	if err := applyConfigFile(fset, writeConfig(t, "allowed-ips: [10.0.0.0/8]\n"), nil); err != nil {
		t.Errorf("removed setting: %v", err)
	}
}

func ptr(s string) *string { return &s }

func TestWriteConfigFile(t *testing.T) {
	p := writeConfig(t, `# my settings
path: ~/Videos # videos
name: Old
# log-level: info
`)
	types := map[string]string{"name": "string", "path": "string", "http-port": "int", "tags": "list", "log-headers": "bool"}
	err := writeConfigFile(p, map[string]*string{
		"name":        ptr("123"),
		"path":        nil,
		"http-port":   ptr("8080"),
		"tags":        ptr("192.168.1.0/24, fd00::/8"),
		"log-headers": ptr("1"),
	}, types)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	for _, want := range []string{"# my settings", "# log-level: info", `name: "123"`, "http-port: 8080", "tags: [192.168.1.0/24, 'fd00::/8']", "log-headers: true"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "path:") {
		t.Errorf("path not removed:\n%s", got)
	}

	// The written file must load back.
	fset := flag.NewFlagSet("test", flag.ContinueOnError)
	name := fset.String("name", "", "")
	ips := fset.String("tags", "", "")
	fset.Int("http-port", 0, "")
	fset.Bool("log-headers", false, "")
	keys := map[string]string{"name": "T1", "tags": "T2", "http-port": "T3", "log-headers": "T4"}
	if err := applyConfigFile(fset, p, keys); err != nil {
		t.Fatal(err)
	}
	if *name != "123" || *ips != "192.168.1.0/24,fd00::/8" {
		t.Errorf("reloaded name=%q tags=%q", *name, *ips)
	}
}

func TestWriteConfigFileNew(t *testing.T) {
	for _, initial := range []string{"", "# only a comment\n"} {
		p := filepath.Join(t.TempDir(), "sub", "just-dlna.yaml")
		if initial != "" {
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(initial), 0o644)
		}
		if err := writeConfigFile(p, map[string]*string{"name": ptr("TV")}, map[string]string{"name": "string"}); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(p)
		if want := initial + "name: TV\n"; string(b) != want {
			t.Errorf("got %q, want %q", b, want)
		}
	}
}

func TestConfigStore(t *testing.T) {
	p := writeConfig(t, "name: From file\n")
	fset := flag.NewFlagSet("test", flag.ContinueOnError)
	fset.String("name", "", "server name [TEST_NAME]")
	fset.String("log-level", "info", "")
	fset.String("ifname", "", "")
	fset.Int("http-port", 1338, "")
	t.Setenv("TEST_LOG_LEVEL", "debug")
	s := &configStore{
		file:     p,
		fset:     fset,
		envKeys:  map[string]string{"name": "TEST_NAME", "log-level": "TEST_LOG_LEVEL", "ifname": "TEST_IFNAME", "http-port": "TEST_HTTP_PORT"},
		order:    []string{"name", "log-level", "ifname", "http-port"},
		explicit: map[string]bool{"ifname": true},
	}
	_, settings, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	src := map[string]string{}
	for _, st := range settings {
		src[st.Name] = st.Source
	}
	want := map[string]string{"name": "file", "log-level": "env", "ifname": "flag", "http-port": "default"}
	for k, v := range want {
		if src[k] != v {
			t.Errorf("%s source = %q, want %q", k, src[k], v)
		}
	}
	if settings[0].Usage != "server name" || *settings[0].FileValue != "From file" {
		t.Errorf("setting %+v", settings[0])
	}

	if err := s.Save(map[string]*string{"log-level": ptr("warn")}); err == nil {
		t.Error("saving an env-locked setting succeeded")
	}
	if err := s.Save(map[string]*string{"http-port": ptr("99999")}); err == nil {
		t.Error("invalid port accepted")
	}
	if err := s.Save(map[string]*string{"http-port": ptr("8080"), "name": nil}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "http-port: 8080\n" {
		t.Errorf("file %q", b)
	}
}

func TestValidInterfaces(t *testing.T) {
	known := map[string]bool{"wlo1": true, "enp1s0": true}
	lookup := func(name string) error {
		if !known[name] {
			return errors.New("no such network interface")
		}
		return nil
	}
	for in, ok := range map[string]bool{
		"":             true,
		"wlo1":         true,
		"wlo1,enp1s0":  true,
		" wlo1 , ":     true,
		"eth9":         false,
		"wlo1,eth9":    false,
		"wlo1,,enp1s0": true,
	} {
		if err := checkInterfaces(in, lookup); (err == nil) != ok {
			t.Errorf("checkInterfaces(%q) = %v, want ok %v", in, err, ok)
		}
	}

	// Against the real interfaces, through the setting spec.
	ifs, err := net.Interfaces()
	if err != nil || len(ifs) == 0 {
		t.Skip("no network interfaces")
	}
	spec := settingSpecs["ifname"]
	if spec.typ != "list" {
		t.Errorf("ifname type = %q, want list", spec.typ)
	}
	if err := spec.check(ifs[0].Name); err != nil {
		t.Errorf("check(%q) = %v", ifs[0].Name, err)
	}
	if err := spec.check(ifs[0].Name + ",no-such-if0"); err == nil {
		t.Error("check accepted a missing interface")
	}
}
