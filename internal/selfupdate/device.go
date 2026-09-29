// Package selfupdate implements the CPDC side of the CPDS self-update
// protocol. This protocol is independent from the existing cpd.proto
// distribution flow.
package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cpdc/internal/netio"
)

const configFileName = "cpdc_config.json"

// DeviceInfo holds the runtime identity needed to answer self-update commands.
type DeviceInfo struct {
	DeviceType string
	IP         string
	Version    string
	RunCommand []string
	ConfigPath string
}

// DetectDeviceType returns the device type encoded in the local
// cpdc_config.<type>.json file name. The active cpdc_config.json is excluded.
//
// configPath is the raw --config-path value and is resolved as follows:
//   - empty: look in the executable's directory;
//   - directory: look in that directory;
//   - file: look in the file's containing directory.
func DetectDeviceType(configPath string) (string, error) {
	configDir, _, err := resolveConfigPaths(configPath)
	if err != nil {
		return "", err
	}
	return detectDeviceType(configDir)
}

func detectDeviceType(configDir string) (string, error) {
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return "", fmt.Errorf("read config dir %s: %w", configDir, err)
	}
	var types []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == configFileName {
			continue
		}
		if !strings.HasPrefix(name, "cpdc_config.") || !strings.HasSuffix(name, ".json") {
			continue
		}
		typeName := strings.TrimSuffix(strings.TrimPrefix(name, "cpdc_config."), ".json")
		if typeName == "" {
			continue
		}
		types = append(types, typeName)
	}
	switch len(types) {
	case 0:
		return "", errors.New("no cpdc_config.<type>.json found")
	case 1:
		return types[0], nil
	default:
		return "", fmt.Errorf("multiple device type configs found: %s", strings.Join(types, ", "))
	}
}

// ReadVersion reads the root "version" field of the active config file. If the
// field is absent or is not a string, it returns an empty version without
// error. configPath is the raw --config-path value and is resolved the same way
// as in DetectDeviceType; a directory resolves to its cpdc_config.json file.
func ReadVersion(configPath string) (string, error) {
	_, configFile, err := resolveConfigPaths(configPath)
	if err != nil {
		return "", err
	}
	return readVersionFile(configFile)
}

func readVersionFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("decode %s: %w", path, err)
	}
	version, _ := raw["version"].(string)
	return version, nil
}

// executablePath resolves the path of the running binary. It is a package
// variable so tests can substitute a fixed path.
var executablePath = os.Executable

// executableDir resolves the directory containing the running binary.
var executableDir = func() (string, error) {
	executable, err := executablePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(executable), nil
}

// resolveConfigPaths maps configPath (the raw --config-path value) to the
// directory that holds config files and to the active config file path.
func resolveConfigPaths(configPath string) (configDir, configFile string, err error) {
	runDir, err := executableDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve executable path: %w", err)
	}
	return resolveConfigPathsIn(configPath, runDir)
}

// resolveConfigPathsIn is resolveConfigPaths with an explicit run directory for
// the empty configPath case.
func resolveConfigPathsIn(configPath, runDir string) (configDir, configFile string, err error) {
	if configPath == "" {
		return runDir, filepath.Join(runDir, configFileName), nil
	}
	if info, err := os.Stat(configPath); err == nil && info.IsDir() {
		return configPath, filepath.Join(configPath, configFileName), nil
	}
	return filepath.Dir(configPath), configPath, nil
}

// DetectLocalIP resolves the local IPv4 address once and caches the result.
// Subsequent calls always return the cached value without re-enumerating
// interfaces.
func DetectLocalIP() (string, error) {
	return defaultIPDetector.detect(detectLocalIP)
}

// ipDetector caches the result of a resolver and returns it on every call.
type ipDetector struct {
	once sync.Once
	ip   string
	err  error
}

func (d *ipDetector) detect(resolve func() (string, error)) (string, error) {
	d.once.Do(func() {
		d.ip, d.err = resolve()
	})
	return d.ip, d.err
}

var defaultIPDetector ipDetector

func detectLocalIP() (string, error) {
	candidates, err := netio.SelectCandidates("")
	if err != nil {
		return "", err
	}
	return candidates[0].IPString(), nil
}

// ParseRunCommand preserves the full command line and extracts the value of
// --config-path. Both "--config-path value" and "--config-path=value" forms
// are recognized, as are the single-dash equivalents accepted by Go's flag
// package.
func ParseRunCommand(args []string) ([]string, string) {
	runCommand := append([]string(nil), args...)
	configPath := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, ok := configPathFlagValue(arg); ok {
			configPath = value
			break
		}
		if arg == "--config-path" || arg == "-config-path" {
			if i+1 < len(args) {
				configPath = args[i+1]
			}
			break
		}
	}
	return runCommand, configPath
}

func configPathFlagValue(arg string) (string, bool) {
	for _, prefix := range []string{"--config-path=", "-config-path="} {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix), true
		}
	}
	return "", false
}
