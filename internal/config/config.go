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

	"github.com/go-viper/mapstructure/v2"
	"github.com/pelletier/go-toml/v2"
	"github.com/srhickma/arc/internal/util"
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

// Config is a parsed arc.conf
type Config struct {
	Dir    string
	Check  CheckConfig
	restic *resticConfig
}

// CheckConfig is the resolved [check] section
type CheckConfig struct {
	HashType string   `mapstructure:"hash-type"`
	Workers  int      `mapstructure:"workers"`
	Ignore   []string `mapstructure:"ignore"`
}

// resticConfig is the parsed [restic] section
type resticConfig struct {
	root     *resticNode
	profiles map[string]*resticNode
}

// resticNode is one node of the [restic] config tree
type resticNode struct {
	flags   map[string]any
	args    []string
	argsSet bool
	env     map[string]string
	subcmds map[string]*resticNode
}

func Load(dir string) (*Config, error) {
	configPath, found := FindConfig(dir)
	if !found {
		return nil, fmt.Errorf("no %s found in %s or any parent directory", FileName, dir)
	}

	return load(configPath)
}

// FindConfig walks up from dir until it finds a directory containing a config file
func FindConfig(dir string) (string, bool) {
	for {
		candidate := filepath.Join(dir, FileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func load(configPath string) (*Config, error) {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	// [check] has a fixed schema; [restic] has arbitrary flag keys, so both
	// come back as raw trees and are interpreted below
	var raw struct {
		Check  map[string]any `toml:"check"`
		Restic map[string]any `toml:"restic"`
	}
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", configPath, err)
	}

	check := CheckConfig{HashType: "sha256", Workers: runtime.NumCPU()}
	if raw.Check != nil {
		if err := decodeStrict(raw.Check, &check); err != nil {
			return nil, fmt.Errorf("parsing %s: [check] %w", configPath, err)
		}
	}

	var restic *resticConfig
	if raw.Restic != nil {
		restic, err = parseRestic(raw.Restic)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: [restic] %w", configPath, err)
		}
	}

	return &Config{
		Dir:    filepath.Dir(configPath),
		Check:  check,
		restic: restic,
	}, nil
}

// decodeStrict decodes a raw TOML table into out, rejecting unknown keys
func decodeStrict(raw map[string]any, out any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:      out,
		ErrorUnused: true,
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
}

func parseRestic(raw map[string]any) (*resticConfig, error) {
	profiles := map[string]*resticNode{}

	if rawProfiles, ok := raw["profiles"]; ok {
		delete(raw, "profiles")
		profilesTable, ok := rawProfiles.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("profiles must be a table")
		}
		for name, rawNode := range profilesTable {
			rawNode, ok := rawNode.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("profiles.%s must be a table", name)
			}
			node, err := parseResticNode(rawNode)
			if err != nil {
				return nil, fmt.Errorf("profiles.%s: %w", name, err)
			}
			profiles[name] = node
		}
	}

	root, err := parseResticNode(raw)
	if err != nil {
		return nil, err
	}

	return &resticConfig{
		profiles: profiles,
		root:     root,
	}, nil
}

func parseResticNode(rawNode map[string]any) (*resticNode, error) {
	node := &resticNode{
		flags:   map[string]any{},
		subcmds: map[string]*resticNode{},
	}

	for key, entry := range rawNode {
		var err error
		switch key {
		case "args":
			err = mapstructure.Decode(entry, &node.args)
			node.argsSet = err == nil
		case "environ":
			err = mapstructure.Decode(entry, &node.env)
		default:
			rawNode, ok := entry.(map[string]any)
			if ok {
				node.subcmds[key], err = parseResticNode(rawNode)
			}

			node.flags[key] = entry
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
	}

	return node, nil
}

// ResticInvocation is a fully-resolved plan for a single `arc restic` call
type ResticInvocation struct {
	// Argv is the argument vector for restic, starting with the subcommand
	Argv []string
	// Env is a list of KEY=VALUE strings to append to the current environment
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
// Flags accumulate across layers (restic resolves duplicates last-wins); args
// are replaced by the last layer that sets them, or by the command-line args if
// any were given.
func (c *Config) ResolveRestic(profile, subcmd string, cliArgs []string) (*ResticInvocation, error) {
	resticConf := c.restic

	// With no config or no subcommand there is nothing to resolve against, so proxy the
	// raw args straight through
	if resticConf == nil || subcmd == "" {
		return &ResticInvocation{Argv: slices.Clone(cliArgs)}, nil
	}

	layers := []*resticNode{resticConf.root, resticConf.root.subcmds[subcmd]}
	if profile != "" {
		profileNode, ok := resticConf.profiles[profile]
		if !ok {
			return nil, fmt.Errorf("unknown restic profile %q", profile)
		}
		layers = append(layers, profileNode, profileNode.subcmds[subcmd])
	}

	flags := map[string]any{}
	env := map[string]string{}
	var args []string
	argsSet := false
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		maps.Copy(flags, layer.flags)
		maps.Copy(env, layer.env)
		if layer.argsSet {
			args, argsSet = layer.args, true
		}
	}

	argv := []string{subcmd}
	for _, key := range slices.Sorted(maps.Keys(flags)) {
		flagArgs, err := flagToArgs(key, flags[key])
		if err != nil {
			return nil, err
		}
		argv = append(argv, flagArgs...)
	}
	switch {
	case len(cliArgs) > 0:
		argv = append(argv, cliArgs...)
	case argsSet:
		for _, arg := range args {
			argv = append(argv, util.ExpandTilde(arg))
		}
	}

	envEntries := make([]string, 0, len(env))
	for _, key := range slices.Sorted(maps.Keys(env)) {
		envEntries = append(envEntries, key+"="+util.ExpandTilde(env[key]))
	}

	return &ResticInvocation{Argv: argv, Env: envEntries}, nil
}

// flagToArgs turns one config flag key and value into restic argv tokens:
// scalars become "--key value", true becomes a bare "--key", false is dropped,
// and arrays repeat the flag
func flagToArgs(key string, value any) ([]string, error) {
	flag := "--" + key
	if len(key) == 1 {
		flag = "-" + key
	}
	switch typed := value.(type) {
	case bool:
		if typed {
			return []string{flag}, nil
		}
		return nil, nil
	case []any:
		var args []string
		for _, element := range typed {
			str, err := scalarString(element)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			args = append(args, flag, util.ExpandTilde(str))
		}
		return args, nil
	default:
		str, err := scalarString(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		return []string{flag, util.ExpandTilde(str)}, nil
	}
}

func scalarString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("expected a scalar, got %T", value)
	}
}
