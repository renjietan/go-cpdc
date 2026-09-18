package client

import (
	"strings"
	"testing"

	cpdv1 "cpdc/gen/cpd/v1"
)

func TestAuthAccumulatorCombinesSplitAssignments(t *testing.T) {
	esn := "123456789012345678901234567890123456789"
	auth := NewAuthAccumulator(esn, []cpdv1.DeviceType{
		cpdv1.DeviceType_DEVICE_TYPE_CCU,
		cpdv1.DeviceType_DEVICE_TYPE_SERVER,
	})

	first := auth.Apply(&cpdv1.AuthNty{Assignments: []*cpdv1.DeviceAssignment{
		{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: esn, NodeId: "node-1", DeviceId: "dc_ccu_1"},
		{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_HF, Esn: strings.Repeat("9", 39), NodeId: "node-1", DeviceId: "dc_hf_1"},
	}})
	if first.Done {
		t.Fatal("authentication completed before all configured types arrived")
	}

	second := auth.Apply(&cpdv1.AuthNty{Assignments: []*cpdv1.DeviceAssignment{
		{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_SERVER, Esn: esn, NodeId: "node-1", DeviceId: "dc_server_1"},
	}})
	if !second.Done || second.Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("decision = %+v", second)
	}
	if second.NodeID != "node-1" || len(second.Bindings) != 2 {
		t.Fatalf("completed binding = %+v", second)
	}
	if second.Bindings[0].DeviceType != cpdv1.DeviceType_DEVICE_TYPE_CCU || second.Bindings[1].DeviceType != cpdv1.DeviceType_DEVICE_TYPE_SERVER {
		t.Fatalf("bindings do not preserve config order: %+v", second.Bindings)
	}
}

func TestAuthAccumulatorRejectsInvalidIdentifiers(t *testing.T) {
	esn := "123456789012345678901234567890123456789"
	auth := NewAuthAccumulator(esn, []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_CCU})
	decision := auth.Apply(&cpdv1.AuthNty{Assignments: []*cpdv1.DeviceAssignment{
		{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: esn, NodeId: strings.Repeat("n", 256), DeviceId: "dc_ccu_1"},
	}})
	if !decision.Done || decision.ErrorCode != cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthAccumulatorReportsConflict(t *testing.T) {
	esn := "123456789012345678901234567890123456789"
	auth := NewAuthAccumulator(esn, []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_CCU, cpdv1.DeviceType_DEVICE_TYPE_SERVER})
	auth.Apply(&cpdv1.AuthNty{Assignments: []*cpdv1.DeviceAssignment{
		{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: esn, NodeId: "node-1", DeviceId: "dc_ccu_1"},
	}})
	decision := auth.Apply(&cpdv1.AuthNty{Assignments: []*cpdv1.DeviceAssignment{
		{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: esn, NodeId: "node-1", DeviceId: "dc_ccu_2"},
	}})
	if !decision.Done || decision.Result != cpdv1.Result_RESULT_FAILED || decision.ErrorCode != cpdv1.ErrorCode_ERROR_CODE_AUTH_CONFLICT {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.Conflict == nil || decision.Conflict.ExistingDeviceId != "dc_ccu_1" || decision.Conflict.ReceivedDeviceId != "dc_ccu_2" {
		t.Fatalf("conflict detail = %+v", decision.Conflict)
	}
}

func TestAuthAccumulatorMissingTypes(t *testing.T) {
	auth := NewAuthAccumulator("123456789012345678901234567890123456789", []cpdv1.DeviceType{
		cpdv1.DeviceType_DEVICE_TYPE_CCU,
		cpdv1.DeviceType_DEVICE_TYPE_HF,
	})
	missing := auth.MissingTypes()
	if len(missing) != 2 || missing[0] != cpdv1.DeviceType_DEVICE_TYPE_CCU || missing[1] != cpdv1.DeviceType_DEVICE_TYPE_HF {
		t.Fatalf("missing = %v", missing)
	}
}

func TestAuthAccumulatorAcceptsCCUAudioAssignment(t *testing.T) {
	esn := "123456789012345678901234567890123456789"
	deviceType := cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO
	auth := NewAuthAccumulator(esn, []cpdv1.DeviceType{deviceType})
	decision := auth.Apply(&cpdv1.AuthNty{Assignments: []*cpdv1.DeviceAssignment{
		{DeviceType: deviceType, Esn: esn, NodeId: "node-1", DeviceId: "dc_ccu_1"},
	}})
	if !decision.Done || decision.Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthAccumulatorAcceptsVehInterAssignment(t *testing.T) {
	esn := "123456789012345678901234567890123456789"
	deviceType := cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER
	auth := NewAuthAccumulator(esn, []cpdv1.DeviceType{deviceType})
	decision := auth.Apply(&cpdv1.AuthNty{Assignments: []*cpdv1.DeviceAssignment{
		{DeviceType: deviceType, Esn: esn, NodeId: "node-1", DeviceId: "dc_VehInter_1"},
	}})
	if !decision.Done || decision.Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("decision = %+v", decision)
	}
}
