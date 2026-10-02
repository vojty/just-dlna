package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const configFileName = "just-dlna.yaml"

// configSearchPaths lists where a config file is looked for when none is
// given explicitly; the first existing one is used.
func configSearchPaths() []string {
	paths := []string{configFileName}
	if d, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(d, "just-dlna", configFileName))
	}
	return append(paths, filepath.Join("/etc/just-dlna", configFileName))
}

// defaultConfigFile is where settings saved in the web UI go when no config
// file exists yet: the user config dir, or else the current directory.
func defaultConfigFile() string {
	if paths := configSearchPaths(); len(paths) > 2 {
		return paths[1]
	}
	return configFileName
}

// findConfigFile returns the first existing file from configSearchPaths, or
// "" when there is none.
func findConfigFile() string {
	for _, p := range configSearchPaths() {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			return p
		}
	}
	return ""
}

// removedSettings are ignored in config files written by older versions.
var removedSettings = map[string]bool{"allowed-ips": true}

// applyConfigFile sets flags from a YAML file whose keys are flag names:
//
//	path: ~/Videos
//	name: Home DLNA
//
// Values from the command line or a flag's environment variable (envKeys
// maps flag names to them) take precedence over the file.
func applyConfigFile(fset *flag.FlagSet, path string, envKeys map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}

	explicit := map[string]bool{}
	fset.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		key, ok := envKeys[name]
		if removedSettings[name] {
			continue
		}
		if !ok {
			return fmt.Errorf("config %s: unknown setting %q", path, name)
		}
		if explicit[name] {
			continue
		}
		if _, ok := os.LookupEnv(key); ok {
			continue
		}
		v := values[name]
		if v == nil {
			continue
		}
		s, err := configString(v)
		if err != nil {
			return fmt.Errorf("config %s: %s: %w", path, name, err)
		}
		if name == "path" || name == "cache" {
			s = expandHome(s)
		}
		if err := fset.Set(name, s); err != nil {
			return fmt.Errorf("config %s: %s: %w", path, name, err)
		}
	}
	return nil
}

// configString converts a YAML value to flag syntax. Lists become comma
// separated values.
func configString(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			s, err := configString(e)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return strings.Join(parts, ","), nil
	case map[string]any:
		return "", errors.New("nested mappings are not supported")
	default:
		return fmt.Sprint(v), nil
	}
}

// expandHome replaces a leading "~" with the user's home directory, as a
// shell would for a command line flag.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}
