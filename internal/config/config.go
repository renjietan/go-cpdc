package config

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/fsutil"
)

const FileName = "cpdc_config.json"

var esnPattern = regexp.MustCompile(`^[0-9]{39}$`)

type Config struct {
	DeviceTypes    []cpdv1.DeviceType
	ESN            string
	TxbzName       string
	TxbzHash       string
	NodeID         string
	DeviceIDs      map[string]string
	ReReadjsonPath string
	TarPath        string
	CachePath      string

	mu   sync.Mutex
	raw  map[string]any
	path string
}

type Snapshot struct {
	TxbzName  string
	TxbzHash  string
	NodeID    string
	DeviceIDs map[string]string
}

// LoadPath loads configuration from configPath. If configPath is empty, the
// default executable-relative cpdc_config.json is used. If configPath points
// to a directory, the fixed file name is appended.
func LoadPath(configPath string, root string, random io.Reader) (*Config, error) {
	path := configPath
	if path == "" {
		path = filepath.Join(root, FileName)
	} else if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, FileName)
	}
	return load(path, random)
}

// Load loads configuration from root/cpdc_config.json, preserving the
// historical behavior of resolving against the executable directory.
func Load(root string, random io.Reader) (*Config, error) {
	return LoadPath("", root, random)
}

func load(path string, random io.Reader) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	var disk struct {
		DeviceTypes    []string          `json:"deviceTypes"`
		ESN            string            `json:"esn"`
		TxbzName       string            `json:"txbzName"`
		TxbzHash       string            `json:"txbzHash"`
		NodeID         string            `json:"nodeId"`
		DeviceIDs      map[string]string `json:"deviceIds"`
		ReReadjsonPath string            `json:"ReReadjsonPath"`
		TarPath        string            `json:"tarPath"`
		CachePath      string            `json:"cachePath"`
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, fmt.Errorf("decode typed config: %w", err)
	}
	types, err := parseTypes(disk.DeviceTypes)
	if err != nil {
		return nil, err
	}
	generated := false
	if disk.ESN == "" {
		if random == nil {
			return nil, errors.New("random source is required to generate ESN")
		}
		disk.ESN, err = GenerateESN(random)
		if err != nil {
			return nil, fmt.Errorf("generate ESN: %w", err)
		}
		generated = true
	}
	if !esnPattern.MatchString(disk.ESN) || strings.Trim(disk.ESN, "0") == "" {
		return nil, errors.New("esn must contain exactly 39 decimal digits")
	}
	if disk.DeviceIDs == nil {
		disk.DeviceIDs = make(map[string]string)
	}
	if disk.TxbzHash != "" {
		decoded, err := hex.DecodeString(disk.TxbzHash)
		if err != nil || len(decoded) != 32 {
			return nil, errors.New("txbzHash must be empty or a 64-character SHA-256 hex string")
		}
	}
	cfg := &Config{
		DeviceTypes:    types,
		ESN:            disk.ESN,
		TxbzName:       disk.TxbzName,
		TxbzHash:       disk.TxbzHash,
		NodeID:         disk.NodeID,
		DeviceIDs:      cloneStrings(disk.DeviceIDs),
		ReReadjsonPath: disk.ReReadjsonPath,
		TarPath:        disk.TarPath,
		CachePath:      disk.CachePath,
		raw:            raw,
		path:           path,
	}
	if generated {
		cfg.raw["esn"] = disk.ESN
		if err := cfg.write(); err != nil {
			return nil, fmt.Errorf("persist generated ESN: %w", err)
		}
	}
	return cfg, nil
}

func (c *Config) Identity() *cpdv1.ClientIdentity {
	return &cpdv1.ClientIdentity{Esn: c.ESN, DeviceTypes: append([]cpdv1.DeviceType(nil), c.DeviceTypes...)}
}

func (c *Config) UpdateSnapshot(snapshot Snapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if snapshot.TxbzName == "" || snapshot.NodeID == "" {
		return errors.New("snapshot file name and node ID are required")
	}
	decoded, err := hex.DecodeString(snapshot.TxbzHash)
	if err != nil || len(decoded) != 32 {
		return errors.New("snapshot hash must be a 64-character SHA-256 hex string")
	}
	for _, deviceType := range c.DeviceTypes {
		if snapshot.DeviceIDs[DeviceTypeName(deviceType)] == "" {
			return fmt.Errorf("snapshot binding missing for %s", DeviceTypeName(deviceType))
		}
	}
	next := cloneAnyMap(c.raw)
	next["deviceTypes"] = deviceTypeNames(c.DeviceTypes)
	next["esn"] = c.ESN
	next["txbzName"] = snapshot.TxbzName
	next["txbzHash"] = snapshot.TxbzHash
	next["nodeId"] = snapshot.NodeID
	next["deviceIds"] = cloneStrings(snapshot.DeviceIDs)
	previous := c.raw
	c.raw = next
	if err := c.write(); err != nil {
		c.raw = previous
		return err
	}
	c.TxbzName = snapshot.TxbzName
	c.TxbzHash = snapshot.TxbzHash
	c.NodeID = snapshot.NodeID
	c.DeviceIDs = cloneStrings(snapshot.DeviceIDs)
	return nil
}

func (c *Config) write() error {
	data, err := json.MarshalIndent(c.raw, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fsutil.AtomicWriteFile(c.path, data, 0o600)
}

func parseTypes(names []string) ([]cpdv1.DeviceType, error) {
	if len(names) == 0 {
		return nil, errors.New("deviceTypes must not be empty")
	}
	seen := make(map[cpdv1.DeviceType]bool, len(names))
	result := make([]cpdv1.DeviceType, 0, len(names))
	radioCount := 0
	server, iec, ccuAudio := false, false, false
	for _, name := range names {
		deviceType, ok := DeviceTypeByName(name)
		if !ok {
			return nil, fmt.Errorf("unknown device type %q", name)
		}
		if seen[deviceType] {
			return nil, fmt.Errorf("duplicate device type %q", name)
		}
		seen[deviceType] = true
		result = append(result, deviceType)
		switch deviceType {
		case cpdv1.DeviceType_DEVICE_TYPE_SERVER:
			server = true
		case cpdv1.DeviceType_DEVICE_TYPE_IEC:
			iec = true
		case cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO:
			ccuAudio = true
		case cpdv1.DeviceType_DEVICE_TYPE_HF,
			cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO,
			cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD,
			cpdv1.DeviceType_DEVICE_TYPE_SMALL_HANDHELD:
			radioCount++
		}
	}
	if server && iec {
		return nil, errors.New("Server and IEC are mutually exclusive")
	}
	if radioCount > 1 {
		return nil, errors.New("only one radio device type may be configured")
	}
	if ccuAudio && len(result) != 1 {
		return nil, errors.New("CCUAudio must be configured as the only device type")
	}
	return result, nil
}

func DeviceTypeByName(name string) (cpdv1.DeviceType, bool) {
	value, ok := map[string]cpdv1.DeviceType{
		"Server":            cpdv1.DeviceType_DEVICE_TYPE_SERVER,
		"HF":                cpdv1.DeviceType_DEVICE_TYPE_HF,
		"MultiBandRadio":    cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO,
		"MultibandHandheld": cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD,
		"CCU":               cpdv1.DeviceType_DEVICE_TYPE_CCU,
		"CCUAudio":          cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO,
		"IEC":               cpdv1.DeviceType_DEVICE_TYPE_IEC,
		"SmallHandheld":     cpdv1.DeviceType_DEVICE_TYPE_SMALL_HANDHELD,
		"VehInter":          cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER,
	}[name]
	return value, ok
}

func DeviceTypeName(deviceType cpdv1.DeviceType) string {
	return map[cpdv1.DeviceType]string{
		cpdv1.DeviceType_DEVICE_TYPE_SERVER:              "Server",
		cpdv1.DeviceType_DEVICE_TYPE_HF:                  "HF",
		cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO:    "MultiBandRadio",
		cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD: "MultibandHandheld",
		cpdv1.DeviceType_DEVICE_TYPE_CCU:                 "CCU",
		cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO:           "CCUAudio",
		cpdv1.DeviceType_DEVICE_TYPE_IEC:                 "IEC",
		cpdv1.DeviceType_DEVICE_TYPE_SMALL_HANDHELD:      "SmallHandheld",
		cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER:           "VehInter",
	}[deviceType]
}

func deviceTypeNames(types []cpdv1.DeviceType) []string {
	names := make([]string, len(types))
	for i, deviceType := range types {
		names[i] = DeviceTypeName(deviceType)
	}
	return names
}

func cloneStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneAnyMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
