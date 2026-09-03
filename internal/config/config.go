package config

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/pelletier/go-toml/v2"
)

const FileName = "arc.conf"

const Template = `[check]
hash-type = "sha256"
workers = 8
ignore = [".DS_Store$"]

[restic.backup]
args = ["."]
exclude-file = ".resticignore"

# [restic.profiles.local]
# repo = "/path/to/repo"

# [restic.profiles.remote]
# repo = "remote-repo-url"
`

// Config is a parsed arc.conf.
type Config struct {
	// Path is the absolute path the config was loaded from.
	Path string
	// Root is the directory containing the config file. For "restic" it is
	// used as the working directory of the restic child process.
	Root string
	// Check holds the resolved [check] section.
	Check CheckConfig

	restic *resticTree
}

// CheckConfig is the resolved [check] section.
type CheckConfig struct {
	HashType string   `mapstructure:"hash-type"`
	Workers  int      `mapstructure:"workers"`
	Ignore   []string `mapstructure:"ignore"`
}

// resticTree is the parsed [restic] section: a root of shared config plus any
// named profiles.
type resticTree struct {
	root     *section
	profiles map[string]*section
}

// section is one node of the [restic] tree. A key whose value is a table is a
// nested subcommand section (except the reserved "environ"); every other key is
// a restic flag.
type section struct {
	flags   map[string]any
	args    []string
	argsSet bool
	env     map[string]string
	subs    map[string]*section
}

// ResolveDir expands a leading ~ and returns an absolute path.
func ResolveDir(path string) (string, error) {
	return filepath.Abs(expand(path))
}

// Discover walks up from start until it finds a directory containing arc.conf.
func Discover(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, FileName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s found in %s or any parent directory", FileName, start)
		}
		dir = parent
	}
}

// Load reads and parses the config file at path.
func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}

	// [check] has a fixed schema; [restic] has arbitrary flag keys, so both
	// come back as raw trees and are interpreted below.
	var raw struct {
		Check  map[string]any `toml:"check"`
		Restic map[string]any `toml:"restic"`
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", abs, err)
	}

	check := CheckConfig{HashType: "sha256", Workers: runtime.NumCPU()}
	if raw.Check != nil {
		if err := decodeStrict(raw.Check, &check); err != nil {
			return nil, fmt.Errorf("parsing %s: [check] %w", abs, err)
		}
	}

	var restic *resticTree
	if raw.Restic != nil {
		restic, err = parseRestic(raw.Restic)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: [restic] %w", abs, err)
		}
	}

	return &Config{Path: abs, Root: filepath.Dir(abs), Check: check, restic: restic}, nil
}

// decodeStrict decodes a raw TOML table into out, rejecting unknown keys.
func decodeStrict(raw map[string]any, out any) error {
	d, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:      out,
		ErrorUnused: true,
	})
	if err != nil {
		return err
	}
	return d.Decode(raw)
}

func parseRestic(raw map[string]any) (*resticTree, error) {
	t := &resticTree{profiles: map[string]*section{}}
	if p, ok := raw["profiles"]; ok {
		delete(raw, "profiles")
		pm, ok := p.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("profiles must be a table")
		}
		for name, v := range pm {
			s, err := parseSection(v)
			if err != nil {
				return nil, fmt.Errorf("profiles.%s: %w", name, err)
			}
			t.profiles[name] = s
		}
	}
	root, err := parseSection(raw)
	if err != nil {
		return nil, err
	}
	t.root = root
	return t, nil
}

func parseSection(v any) (*section, error) {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a table")
	}
	s := &section{flags: map[string]any{}, subs: map[string]*section{}}
	for k, val := range raw {
		var err error
		switch {
		case k == "args":
			err = mapstructure.Decode(val, &s.args)
			s.argsSet = err == nil
		case k == "environ":
			err = mapstructure.Decode(val, &s.env)
		case isTable(val):
			s.subs[k], err = parseSection(val)
		default:
			s.flags[k] = val
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
	}
	return s, nil
}

func isTable(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

// ResticInvocation is a fully-resolved plan for a single `arc restic` call.
type ResticInvocation struct {
	// Argv is the argument vector for restic, starting with the subcommand.
	Argv []string
	// Env is a list of KEY=VALUE strings to append to the current environment.
	Env []string
}

// ResolveRestic merges the [restic] config layers for the given profile and
// subcommand and combines them with args passed on the command line.
//
// Layers are applied in ascending precedence:
//
//	[restic]
//	[restic.<sub>]
//	[restic.profiles.<profile>]
//	[restic.profiles.<profile>.<sub>]
//	command-line args
//
// A later layer overrides an earlier one per flag key and for args. Command-line
// args, if any, replace the config args entirely.
func (c *Config) ResolveRestic(profile, sub string, cliArgs []string) (*ResticInvocation, error) {
	t := c.restic
	if t == nil {
		return nil, fmt.Errorf("%s has no [restic] section", c.Path)
	}

	// With no subcommand there is nothing to resolve against; proxy the raw
	// args straight through (e.g. `arc restic --help`).
	if sub == "" {
		return &ResticInvocation{Argv: slices.Clone(cliArgs)}, nil
	}

	layers := []*section{t.root, t.root.subs[sub]}
	if profile != "" {
		p, ok := t.profiles[profile]
		if !ok {
			return nil, fmt.Errorf("unknown restic profile %q", profile)
		}
		layers = append(layers, p, p.subs[sub])
	}

	flags := map[string]any{}
	env := map[string]string{}
	var args []string
	argsSet := false
	for _, l := range layers {
		if l == nil {
			continue
		}
		maps.Copy(flags, l.flags)
		maps.Copy(env, l.env)
		if l.argsSet {
			args, argsSet = l.args, true
		}
	}

	argv := []string{sub}
	for _, k := range slices.Sorted(maps.Keys(flags)) {
		a, err := flagToArgs(k, flags[k])
		if err != nil {
			return nil, err
		}
		argv = append(argv, a...)
	}
	switch {
	case len(cliArgs) > 0:
		argv = append(argv, cliArgs...)
	case argsSet:
		for _, a := range args {
			argv = append(argv, expand(a))
		}
	}

	out := make([]string, 0, len(env))
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+expand(env[k]))
	}

	return &ResticInvocation{Argv: argv, Env: out}, nil
}

// flagToArgs turns one config flag key and value into restic argv tokens:
// scalars become "--key value", true becomes a bare "--key", false is dropped,
// and arrays repeat the flag.
func flagToArgs(key string, val any) ([]string, error) {
	flag := "--" + key
	if len(key) == 1 {
		flag = "-" + key
	}
	switch v := val.(type) {
	case bool:
		if v {
			return []string{flag}, nil
		}
		return nil, nil
	case []any:
		var out []string
		for _, e := range v {
			s, err := scalarString(e)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out = append(out, flag, expand(s))
		}
		return out, nil
	default:
		s, err := scalarString(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		return []string{flag, expand(s)}, nil
	}
}

func scalarString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("expected a scalar, got %T", v)
	}
}

// expand resolves a leading ~ to the user's home directory.
func expand(s string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	if s == "~" {
		return home
	}
	return filepath.Join(home, s[2:])
}
