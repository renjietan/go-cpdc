package client

import (
	"fmt"
	"strings"
	"unicode/utf8"

	cpdv1 "cpdc/gen/cpd/v1"
)

type AuthDecision struct {
	Done      bool
	Result    cpdv1.Result
	ErrorCode cpdv1.ErrorCode
	NodeID    string
	Bindings  []*cpdv1.DeviceBinding
	Conflict  *cpdv1.AuthConflictDetail
}

type AuthAccumulator struct {
	esn       string
	types     []cpdv1.DeviceType
	supported map[cpdv1.DeviceType]bool
	nodeID    string
	bindings  map[cpdv1.DeviceType]string
	failed    *AuthDecision
}

func NewAuthAccumulator(esn string, types []cpdv1.DeviceType) *AuthAccumulator {
	supported := make(map[cpdv1.DeviceType]bool, len(types))
	for _, deviceType := range types {
		supported[deviceType] = true
	}
	return &AuthAccumulator{
		esn: esn, types: append([]cpdv1.DeviceType(nil), types...),
		supported: supported, bindings: make(map[cpdv1.DeviceType]string),
	}
}

func (a *AuthAccumulator) Apply(message *cpdv1.AuthNty) AuthDecision {
	if a.failed != nil {
		return *a.failed
	}
	if message == nil || len(message.GetAssignments()) == 0 {
		return a.fail(cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE, nil)
	}
	for _, assignment := range message.GetAssignments() {
		if assignment == nil || !validAuthESN(assignment.GetEsn()) || !validAuthIdentifier(assignment.GetNodeId()) ||
			!validAuthIdentifier(assignment.GetDeviceId()) || !knownDeviceType(assignment.GetDeviceType()) {
			return a.fail(cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE, nil)
		}
		if assignment.GetEsn() != a.esn {
			continue
		}
		if !a.supported[assignment.GetDeviceType()] {
			return a.fail(cpdv1.ErrorCode_ERROR_CODE_AUTH_ASSIGNMENT_CONFLICT, nil)
		}
		if a.nodeID != "" && a.nodeID != assignment.GetNodeId() {
			return a.fail(cpdv1.ErrorCode_ERROR_CODE_AUTH_ASSIGNMENT_CONFLICT, nil)
		}
		existing, present := a.bindings[assignment.GetDeviceType()]
		if present && existing != assignment.GetDeviceId() {
			return a.fail(cpdv1.ErrorCode_ERROR_CODE_AUTH_CONFLICT, &cpdv1.AuthConflictDetail{
				DeviceType: assignment.GetDeviceType(), ExistingNodeId: a.nodeID, ExistingDeviceId: existing,
				ReceivedNodeId: assignment.GetNodeId(), ReceivedDeviceId: assignment.GetDeviceId(),
			})
		}
		a.nodeID = assignment.GetNodeId()
		a.bindings[assignment.GetDeviceType()] = assignment.GetDeviceId()
	}
	if len(a.bindings) != len(a.types) {
		return AuthDecision{}
	}
	bindings := make([]*cpdv1.DeviceBinding, len(a.types))
	for i, deviceType := range a.types {
		deviceID := a.bindings[deviceType]
		if deviceID == "" {
			return AuthDecision{}
		}
		bindings[i] = &cpdv1.DeviceBinding{DeviceType: deviceType, DeviceId: deviceID}
	}
	return AuthDecision{Done: true, Result: cpdv1.Result_RESULT_SUCCESS, NodeID: a.nodeID, Bindings: bindings}
}

func validAuthESN(esn string) bool {
	if len(esn) != 39 {
		return false
	}
	for index := range esn {
		if esn[index] < '0' || esn[index] > '9' {
			return false
		}
	}
	return true
}

func validAuthIdentifier(value string) bool {
	return value != "" && utf8.ValidString(value) && len([]byte(value)) <= 255 && !strings.ContainsAny(value, "/\\:\x00")
}

func knownDeviceType(deviceType cpdv1.DeviceType) bool {
	switch deviceType {
	case cpdv1.DeviceType_DEVICE_TYPE_SERVER,
		cpdv1.DeviceType_DEVICE_TYPE_HF,
		cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO,
		cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD,
		cpdv1.DeviceType_DEVICE_TYPE_CCU,
		cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO,
		cpdv1.DeviceType_DEVICE_TYPE_IEC,
		cpdv1.DeviceType_DEVICE_TYPE_SMALL_HANDHELD,
		cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER:
		return true
	default:
		return false
	}
}

func (a *AuthAccumulator) MissingTypes() []cpdv1.DeviceType {
	missing := make([]cpdv1.DeviceType, 0, len(a.types)-len(a.bindings))
	for _, deviceType := range a.types {
		if a.bindings[deviceType] == "" {
			missing = append(missing, deviceType)
		}
	}
	return missing
}

func (a *AuthAccumulator) BindingMap() map[cpdv1.DeviceType]string {
	result := make(map[cpdv1.DeviceType]string, len(a.bindings))
	for deviceType, deviceID := range a.bindings {
		result[deviceType] = deviceID
	}
	return result
}

func (a *AuthAccumulator) fail(code cpdv1.ErrorCode, conflict *cpdv1.AuthConflictDetail) AuthDecision {
	decision := AuthDecision{Done: true, Result: cpdv1.Result_RESULT_FAILED, ErrorCode: code, Conflict: conflict}
	a.failed = &decision
	return decision
}

func (a *AuthAccumulator) String() string {
	return fmt.Sprintf("node=%s bindings=%d/%d", a.nodeID, len(a.bindings), len(a.types))
}
