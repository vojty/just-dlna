package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"just-dlna/internal/admin"
)

// settingSpec describes how the admin UI edits a setting.
type settingSpec struct {
	typ      string // string, int, bool, enum or list
	options  []string
	validate func(string) error
}

var logLevels = []string{"debug", "info", "warn", "error"}

func validPort(min int) func(string) error {
	return func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil || n < min || n > 65535 {
			return fmt.Errorf("must be a port number between %d and 65535", min)
		}
		return nil
	}
}

func validDir(s string) error {
	fi, err := os.Stat(expandHome(s))
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("not a directory")
	}
	return nil
}

// settingSpecs lists the settings editable in the admin UI. Flags missing
// here (config, ui-dir) are not shown.
var settingSpecs = map[string]settingSpec{
	"path":          {typ: "string", validate: validDir},
	"name":          {typ: "string"},
	"http-port":     {typ: "int", validate: validPort(1)},
	"media-port":    {typ: "int", validate: validPort(1)},
	"ui-port":       {typ: "int", validate: validPort(0)},
	"cache":         {typ: "string"},
	"sub-charset":   {typ: "string"},
	"prefetch-subs": {typ: "bool"},
	"log-level":     {typ: "enum", options: logLevels},
	"dms-log-level": {typ: "enum", options: logLevels},
	"log-format":    {typ: "enum", options: []string{"auto", "pretty", "text", "json"}},
	"log-headers":   {typ: "bool"},
	"ifname":        {typ: "string", validate: validInterface},
	"allowed-ips": {typ: "list", validate: func(s string) error {
		_, err := parseNets(s)
		return err
	}},
}

func validInterface(s string) error {
	if s == "" {
		return nil
	}
	_, err := net.InterfaceByName(s)
	return err
}

func (sp settingSpec) check(v string) error {
	switch sp.typ {
	case "int":
		if _, err := strconv.Atoi(v); err != nil {
			return errors.New("must be a whole number")
		}
	case "bool":
		if _, err := strconv.ParseBool(v); err != nil {
			return errors.New("must be true or false")
		}
	case "enum":
		if !slices.Contains(sp.options, v) {
			return fmt.Errorf("must be one of %s", strings.Join(sp.options, ", "))
		}
	}
	if sp.validate != nil {
		return sp.validate(v)
	}
	return nil
}

// configStore reads and writes the YAML config file for the admin UI. Values
// of the running process come from the flag set; saved changes take effect
// after a restart.
type configStore struct {
	mu       sync.Mutex
	file     string
	fset     *flag.FlagSet
	envKeys  map[string]string
	order    []string
	explicit map[string]bool // set on the command line
}

func (s *configStore) source(name string, file map[string]any) string {
	switch {
	case s.explicit[name]:
		return "flag"
	case s.envSet(name):
		return "env"
	case file[name] != nil:
		return "file"
	default:
		return "default"
	}
}

func (s *configStore) envSet(name string) bool {
	_, ok := os.LookupEnv(s.envKeys[name])
	return ok
}

func (s *configStore) readFile() (map[string]any, error) {
	data, err := os.ReadFile(s.file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("config %s: %w", s.file, err)
	}
	return values, nil
}

func (s *configStore) Settings() (string, []admin.Setting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readFile()
	if err != nil {
		return s.file, nil, err
	}
	var out []admin.Setting
	for _, name := range s.order {
		spec, ok := settingSpecs[name]
		if !ok {
			continue
		}
		f := s.fset.Lookup(name)
		st := admin.Setting{
			Name:    name,
			Env:     s.envKeys[name],
			Usage:   strings.TrimSuffix(f.Usage, " ["+s.envKeys[name]+"]"),
			Type:    spec.typ,
			Options: spec.options,
			Value:   f.Value.String(),
			Default: f.DefValue,
			Source:  s.source(name, file),
		}
		st.Locked = st.Source == "flag" || st.Source == "env"
		if v := file[name]; v != nil {
			if fv, err := configString(v); err == nil {
				st.FileValue = &fv
			}
		}
		out = append(out, st)
	}
	return s.file, out, nil
}

func (s *configStore) Save(updates map[string]*string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := map[string]string{}
	for name, v := range updates {
		spec, ok := settingSpecs[name]
		if !ok {
			return admin.Invalid(fmt.Errorf("unknown setting %q", name))
		}
		if s.explicit[name] || s.envSet(name) {
			return admin.Conflict(fmt.Errorf("%s is set by a command line flag or the %s environment variable", name, s.envKeys[name]))
		}
		if v != nil {
			*v = strings.TrimSpace(*v)
			if err := spec.check(*v); err != nil {
				return admin.Invalid(fmt.Errorf("%s: %w", name, err))
			}
		}
		types[name] = spec.typ
	}
	return writeConfigFile(s.file, updates, types)
}

// writeConfigFile sets or (for nil values) removes top-level keys of the YAML
// file at path, keeping the rest of the file and its comments. types gives the
// setting type for each key, see settingSpec.
func writeConfigFile(path string, updates map[string]*string, types map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	// An empty or comment-only file has no document node: keep its text and
	// append a new mapping below it.
	prefix := ""
	if doc.Kind == 0 {
		prefix = string(data)
		if prefix != "" && !strings.HasSuffix(prefix, "\n") {
			prefix += "\n"
		}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return fmt.Errorf("config %s: top level is not a mapping", path)
	}

	names := make([]string, 0, len(updates))
	for name := range updates {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		i := -1
		for j := 0; j < len(m.Content); j += 2 {
			if m.Content[j].Value == name {
				i = j
				break
			}
		}
		v := updates[name]
		if v == nil {
			if i >= 0 {
				// Keep comments above the removed key, e.g. a file header.
				if hc := m.Content[i].HeadComment; hc != "" {
					if i+2 < len(m.Content) {
						next := m.Content[i+2]
						next.HeadComment = strings.TrimSpace(hc + "\n" + next.HeadComment)
					} else {
						m.FootComment = strings.TrimSpace(hc + "\n" + m.FootComment)
					}
				}
				m.Content = slices.Delete(m.Content, i, i+2)
			}
			continue
		}
		val := valueNode(*v, types[name])
		if i >= 0 {
			old := m.Content[i+1]
			val.LineComment = old.LineComment
			m.Content[i+1] = val
		} else {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, val)
		}
	}

	var out strings.Builder
	out.WriteString(prefix)
	if prefix == "" || len(m.Content) > 0 {
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Written in place rather than via rename, so a bind-mounted file works.
	return os.WriteFile(path, []byte(out.String()), 0o644)
}

func valueNode(v, typ string) *yaml.Node {
	switch typ {
	case "list":
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part})
			}
		}
		return seq
	case "int":
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: v}
	case "bool":
		b, _ := strconv.ParseBool(v)
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	}
}
