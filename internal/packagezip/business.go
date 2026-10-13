package packagezip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	cpdv1 "cpdc/gen/cpd/v1"
)

type Node struct {
	ID      string
	Devices []DeviceRef
}

type DeviceRef struct {
	Type     cpdv1.DeviceType
	DeviceID string
}

type Device struct {
	ID      string
	Type    cpdv1.DeviceType
	Path    string
	Alias   string
	Subnets []string
}

var requiredDirectories = []string{"0_contacts", "3_device_config", "4_net_node", "6_unit"}

func (p *Package) validateBusiness(ctx context.Context) error {
	for _, directory := range requiredDirectories {
		if !p.HasDirectory(directory) {
			return invalid(directory, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("required business directory is missing"))
		}
	}
	documents := make(map[string]map[string]any)
	for _, name := range p.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "local_node.json" {
			continue
		}
		root := strings.SplitN(name, "/", 2)[0]
		if root == "0_contacts" && !strings.HasPrefix(path.Base(name), "contacts_") {
			continue
		}
		indexedDirectory := root == "0_contacts" || root == "3_device_config" || root == "4_net_node" || root == "6_unit"
		if !strings.EqualFold(path.Ext(name), ".json") {
			if indexedDirectory {
				return invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("indexed business directory contains a non-JSON file"))
			}
			continue
		}
		if root < "0_contacts" || root > "6_unit" {
			continue
		}
		value, err := p.decodeJSON(name)
		if err != nil {
			return err
		}
		if indexedDirectory {
			document, ok := object(value)
			if !ok {
				return invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("indexed JSON root must be an object"))
			}
			documents[name] = document
			// Real CCU and Server device files have no File block. When device
			// metadata is present it is still validated, while contacts, nodes,
			// and units always require their indexing Guid.
			_, hasFile := document["File"]
			if root != "3_device_config" || hasFile {
				if _, err := normalizeGuid(document); err != nil {
					return invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
				}
			}
		}
	}
	units, err := indexFlat(documents, "6_unit")
	if err != nil {
		return err
	}
	nodes, err := indexFlat(documents, "4_net_node")
	if err != nil {
		return err
	}
	deviceDocs, err := indexFlat(documents, "3_device_config")
	if err != nil {
		return err
	}
	contacts, err := indexFlat(documents, "0_contacts")
	if err != nil {
		return err
	}
	if len(units) == 0 || len(nodes) == 0 || len(contacts) == 0 {
		return invalid("", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("business indexes must not be empty"))
	}
	for deviceID, document := range deviceDocs {
		deviceType, known := inferDeviceType(deviceID)
		if !known {
			continue
		}
		device, err := p.parseDevice(deviceID, deviceType, document)
		if err != nil {
			return err
		}
		p.Devices[deviceID] = device
	}
	for contactPath, contact := range contacts {
		tree, ok := contact["UnitTree"]
		if !ok {
			return invalid(contactPath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("UnitTree is missing"))
		}
		for _, unitID := range collectPrefixedStrings(tree, "unit_") {
			if units[unitID] == nil {
				return invalid(contactPath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, fmt.Errorf("unit reference %s is missing", unitID))
			}
		}
		for _, nodeID := range collectPrefixedStrings(tree, "nn_") {
			if nodes[nodeID] == nil {
				return invalid(contactPath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, fmt.Errorf("node reference %s is missing", nodeID))
			}
		}
	}
	for nodeID, document := range nodes {
		if len([]byte(nodeID)) > MaxComponentBytes {
			return invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("node ID exceeds limit"))
		}
		node, err := p.parseNode(nodeID, document, deviceDocs)
		if err != nil {
			return err
		}
		p.Nodes[nodeID] = node
	}
	return nil
}

func inferDeviceType(deviceID string) (cpdv1.DeviceType, bool) {
	definitions := []struct {
		prefix     string
		deviceType cpdv1.DeviceType
	}{
		{"dc_teds_", cpdv1.DeviceType_DEVICE_TYPE_SERVER},
		{"dc_IEC_", cpdv1.DeviceType_DEVICE_TYPE_IEC},
		{"dc_ccu_", cpdv1.DeviceType_DEVICE_TYPE_CCU},
		{"dc_VehInter_", cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER},
		{"dc_MMR200_", cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO},
		{"dc_PMR200_", cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD},
		{"dc_MR9360_", cpdv1.DeviceType_DEVICE_TYPE_HF},
		{"dc_PRR206_", cpdv1.DeviceType_DEVICE_TYPE_SMALL_HANDHELD},
	}
	for _, definition := range definitions {
		if strings.HasPrefix(deviceID, definition.prefix) {
			return definition.deviceType, true
		}
	}
	return cpdv1.DeviceType_DEVICE_TYPE_UNSPECIFIED, false
}

func (p *Package) parseNode(nodeID string, document map[string]any, deviceDocs map[string]map[string]any) (*Node, error) {
	system, ok := object(document["SystemConfiguration"])
	if !ok {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("SystemConfiguration must be an object"))
	}
	node := &Node{ID: nodeID}
	seen := make(map[string]cpdv1.DeviceType)
	tedsSeen, iecSeen := false, false
	add := func(deviceType cpdv1.DeviceType, expectedPrefix string, ids []string) error {
		for _, deviceID := range ids {
			if len([]byte(deviceID)) > MaxComponentBytes || !utf8.ValidString(deviceID) || !strings.HasPrefix(deviceID, expectedPrefix) || strings.ContainsAny(deviceID, "/\\:\x00") {
				return invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, fmt.Errorf("invalid device reference %s", deviceID))
			}
			if previous, exists := seen[deviceID]; exists {
				return invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, fmt.Errorf("duplicate device reference %s (%s)", deviceID, previous))
			}
			document := deviceDocs[deviceID]
			if document == nil {
				return invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, fmt.Errorf("device reference %s is missing", deviceID))
			}
			seen[deviceID] = deviceType
			node.Devices = append(node.Devices, DeviceRef{Type: deviceType, DeviceID: deviceID})
			device, err := p.parseDevice(deviceID, deviceType, document)
			if err != nil {
				return err
			}
			if existing := p.Devices[deviceID]; existing != nil && existing.Type != deviceType {
				return invalid(device.Path, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("device is referenced with different types"))
			}
			p.Devices[deviceID] = device
		}
		return nil
	}
	lanPrimary, err := optionalObject(system, "LANPrimary")
	if err != nil {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
	}
	tedsIDs, err := optionalStringList(lanPrimary, "TEDS")
	if err != nil {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
	}
	for _, id := range tedsIDs {
		switch {
		case strings.HasPrefix(id, "dc_teds_"):
			tedsSeen = true
			if err := add(cpdv1.DeviceType_DEVICE_TYPE_SERVER, "dc_teds_", []string{id}); err != nil {
				return nil, err
			}
		case strings.HasPrefix(id, "dc_IEC_"):
			iecSeen = true
			if err := add(cpdv1.DeviceType_DEVICE_TYPE_IEC, "dc_IEC_", []string{id}); err != nil {
				return nil, err
			}
		default:
			return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, fmt.Errorf("unknown LANPrimary.TEDS device %s", id))
		}
	}
	if tedsSeen && iecSeen {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("TEDS and IEC are mutually exclusive"))
	}
	lanMember, err := optionalObject(system, "LANMember")
	if err != nil {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
	}
	ccuIDs, err := optionalStringList(lanMember, "CCU")
	if err != nil {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
	}
	if err := add(cpdv1.DeviceType_DEVICE_TYPE_CCU, "dc_ccu_", ccuIDs); err != nil {
		return nil, err
	}
	vehIDs, err := optionalStringList(lanMember, "VehInter")
	if err != nil {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
	}
	if err := add(cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER, "dc_VehInter_", vehIDs); err != nil {
		return nil, err
	}
	radio, err := optionalObject(system, "Radio")
	if err != nil {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
	}
	radioTypes := []struct {
		key, prefix string
		deviceType  cpdv1.DeviceType
	}{
		{"MMR200", "dc_MMR200_", cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO},
		{"PMR200", "dc_PMR200_", cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD},
		{"MR9360", "dc_MR9360_", cpdv1.DeviceType_DEVICE_TYPE_HF},
		{"PRR206", "dc_PRR206_", cpdv1.DeviceType_DEVICE_TYPE_SMALL_HANDHELD},
	}
	for _, definition := range radioTypes {
		ids, err := optionalStringList(radio, definition.key)
		if err != nil {
			return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
		}
		if err := add(definition.deviceType, definition.prefix, ids); err != nil {
			return nil, err
		}
	}
	if len(node.Devices) > MaxDevicesPerNode {
		return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("node device count exceeds limit"))
	}
	return node, nil
}

func (p *Package) parseDevice(deviceID string, deviceType cpdv1.DeviceType, document map[string]any) (*Device, error) {
	filePath := "3_device_config/" + deviceID + ".json"
	device := &Device{ID: deviceID, Type: deviceType, Path: filePath}
	switch deviceType {
	case cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO,
		cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD,
		cpdv1.DeviceType_DEVICE_TYPE_HF,
		cpdv1.DeviceType_DEVICE_TYPE_SMALL_HANDHELD:
		if !p.HasDirectory("1_resource") || !p.HasDirectory("2_radio_subnet") {
			return nil, invalid(filePath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("radio resource directories are missing"))
		}
		channels, ok := object(document["Channels"])
		if !ok {
			return nil, invalid(filePath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("Channels must be an object"))
		}
		unique := make(map[string]bool)
		for _, channelValue := range channels {
			channel, ok := object(channelValue)
			if !ok {
				return nil, invalid(filePath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("channel must be an object"))
			}
			subnet, ok := channel["Subnet"].(string)
			if !ok || subnet == "" || strings.ContainsAny(subnet, "/\\:\x00") || len([]byte(subnet)) > MaxComponentBytes {
				return nil, invalid(filePath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("channel Subnet is invalid"))
			}
			if !p.Has("2_radio_subnet/" + subnet + ".json") {
				return nil, invalid(filePath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, fmt.Errorf("subnet reference %s is missing", subnet))
			}
			unique[subnet] = true
		}
		for subnet := range unique {
			device.Subnets = append(device.Subnets, subnet)
		}
		sort.Strings(device.Subnets)
		if aliasValue, exists := document["Alias"]; exists {
			alias, ok := aliasValue.(string)
			if !ok || len([]byte(alias)) > MaxAliasBytes || strings.ContainsAny(alias, "\x00\r\n") {
				return nil, invalid(filePath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("Alias is invalid"))
			}
			device.Alias = alias
		}
		if (deviceType == cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO || deviceType == cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD) && device.Alias == "" {
			return nil, invalid(filePath, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("Alias is required"))
		}
	}
	return device, nil
}

func (p *Package) decodeJSON(name string) (any, error) {
	reader, err := p.Open(name)
	if err != nil {
		return nil, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("cannot open JSON"))
	}
	defer reader.Close()
	decoder := json.NewDecoder(io.LimitReader(reader, MaxEntrySize+1))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("invalid JSON syntax"))
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("JSON contains trailing data"))
	}
	return value, nil
}

func normalizeGuid(document map[string]any) (string, error) {
	file, ok := object(document["File"])
	if !ok {
		return "", errors.New("File must be an object")
	}
	switch guid := file["Guid"].(type) {
	case string:
		if guid == "" {
			return "", errors.New("File.Guid must not be empty")
		}
		return guid, nil
	case json.Number:
		if guid.String() == "" {
			return "", errors.New("File.Guid must not be empty")
		}
		return guid.String(), nil
	default:
		return "", errors.New("File.Guid must be a string or number")
	}
}

func indexFlat(documents map[string]map[string]any, directory string) (map[string]map[string]any, error) {
	index := make(map[string]map[string]any)
	prefix := directory + "/"
	for name, document := range documents {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		relative := strings.TrimPrefix(name, prefix)
		if strings.Contains(relative, "/") || !strings.EqualFold(path.Ext(relative), ".json") {
			return nil, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("business index must contain flat JSON files"))
		}
		id := strings.TrimSuffix(relative, path.Ext(relative))
		if index[id] != nil {
			return nil, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("duplicate business ID"))
		}
		index[id] = document
	}
	return index, nil
}

func object(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func optionalObject(parent map[string]any, key string) (map[string]any, error) {
	value, exists := parent[key]
	if !exists {
		return map[string]any{}, nil
	}
	result, ok := object(value)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return result, nil
}

func optionalStringList(parent map[string]any, key string) ([]string, error) {
	value, exists := parent[key]
	if !exists {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", key)
	}
	result := make([]string, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", key, i)
		}
		result[i] = strings.TrimSuffix(text, ".json")
	}
	return result, nil
}

func collectPrefixedStrings(value any, prefix string) []string {
	unique := make(map[string]bool)
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case string:
			candidate := strings.TrimSuffix(typed, ".json")
			if strings.HasPrefix(candidate, prefix) {
				unique[candidate] = true
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	result := make([]string, 0, len(unique))
	for item := range unique {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}
