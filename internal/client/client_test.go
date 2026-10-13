package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/config"
	"google.golang.org/protobuf/proto"
)

func TestClientDiscoveryAndSplitAuthentication(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU", "TEDS"})
	client := newTestClient(t, root, cfg, nil)
	now := time.Unix(100, 0)
	sessionID := testID(10)
	discoverID := testID(11)

	replies := client.Handle(&cpdv1.Packet{
		SessionId: sessionID, MessageId: discoverID,
		Body: &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}},
	}, now)
	if len(replies) != 1 || replies[0].GetDiscoverRsp() == nil {
		t.Fatalf("discover replies = %v", replies)
	}
	discover := replies[0].GetDiscoverRsp()
	if discover.Esn != cfg.ESN || !bytes.Equal(discover.InstanceNonce, bytes.Repeat([]byte{9}, 16)) || discover.CurrentIp != "10.0.0.2" || discover.SubnetMask != "255.255.255.0" {
		t.Fatalf("discover body = %+v", discover)
	}
	if !bytes.Equal(replies[0].MessageId, discoverID) {
		t.Fatal("DiscoverRsp did not copy triggering message_id")
	}

	first := client.Handle(authPacket(sessionID, testID(12), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_1", DeviceId: "dc_ccu_1"}), now.Add(time.Second))
	if len(first) != 0 {
		t.Fatalf("partial auth replied: %v", first)
	}
	secondID := testID(13)
	second := client.Handle(authPacket(sessionID, secondID, cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_SERVER, Esn: cfg.ESN, NodeId: "nn_1", DeviceId: "dc_server_1"}), now.Add(2*time.Second))
	if len(second) != 1 || second[0].GetAuthRsp().GetResult() != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("auth replies = %v", second)
	}
	if !bytes.Equal(second[0].MessageId, secondID) || len(second[0].GetAuthRsp().GetBindings()) != 2 {
		t.Fatalf("auth response = %v", second[0])
	}
}

func TestClientTransferParseRetryAndAck(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	parseCalls := 0
	parse := func(_ ParseRequest) ParseOutcome {
		parseCalls++
		return ParseOutcome{
			Result: cpdv1.Result_RESULT_SUCCESS,
			TypeResults: []*cpdv1.ParseTypeResult{{
				DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, DeviceId: "dc_ccu_1",
				Result: cpdv1.Result_RESULT_SUCCESS,
			}},
		}
	}
	client := newTestClient(t, root, cfg, parse)
	now := time.Unix(200, 0)
	sessionID := testID(20)
	authenticateOne(t, client, cfg, sessionID, now)

	data := []byte("small package")
	hash := sha256.Sum256(data)
	start := &cpdv1.TransferStartNty{
		FileName: "txbz_small.zip", FileSize: uint64(len(data)), FileSha256: hash[:],
		ExpandedSize: 100, RequiredWorkspace: 200, ChunkSize: ChunkSize, TotalChunks: 1,
	}
	startID := testID(23)
	if replies := client.Handle(packetWithBody(sessionID, startID, &cpdv1.Packet_TransferStartNty{TransferStartNty: start}), now.Add(2*time.Second)); len(replies) != 0 {
		t.Fatalf("start replies = %v", replies)
	}
	chunkID := testID(24)
	chunkReplies := client.Handle(packetWithBody(sessionID, chunkID, &cpdv1.Packet_TransferChunkNty{TransferChunkNty: &cpdv1.TransferChunkNty{
		ChunkIndex: 0, Payload: data, PayloadCrc32: crc(data),
	}}), now.Add(3*time.Second))
	if len(chunkReplies) != 1 || chunkReplies[0].GetTransferProgressRsp().GetPercent() != 100 {
		t.Fatalf("chunk replies = %v", chunkReplies)
	}
	endID := testID(25)
	endReplies := client.Handle(packetWithBody(sessionID, endID, &cpdv1.Packet_TransferEndNty{TransferEndNty: &cpdv1.TransferEndNty{}}), now.Add(4*time.Second))
	if len(endReplies) != 1 || endReplies[0].GetTransferCompleteRsp().GetResult() != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("end replies = %v", endReplies)
	}
	if duplicateEnd := client.Handle(packetWithBody(sessionID, testID(29), &cpdv1.Packet_TransferEndNty{TransferEndNty: &cpdv1.TransferEndNty{}}), now.Add(4*time.Second)); len(duplicateEnd) != 0 {
		t.Fatalf("transfer-complete client replied to duplicate END: %v", duplicateEnd)
	}

	parsePacket := waitForParsePacket(t, client, now.Add(4*time.Second))
	if parseCalls != 1 || parsePacket.GetParseCompleteReq().GetResult() != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("parse calls=%d packet=%v", parseCalls, parsePacket)
	}
	if replies := client.Handle(packetWithBody(sessionID, testID(27), &cpdv1.Packet_TransferEndNty{TransferEndNty: &cpdv1.TransferEndNty{}}), now.Add(4500*time.Millisecond)); len(replies) != 0 {
		t.Fatalf("reporting client handled TRANSFER_END: %v", replies)
	}
	if replies := client.Handle(authPacket(sessionID, testID(28), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_1", DeviceId: "dc_ccu_1"}), now.Add(4500*time.Millisecond)); len(replies) != 0 {
		t.Fatalf("reporting client handled AUTH_NTY: %v", replies)
	}
	retry := client.Tick(now.Add(5 * time.Second))
	if len(retry) != 1 || !proto.Equal(retry[0], parsePacket) {
		t.Fatalf("retry changed logical message: %v vs %v", retry, parsePacket)
	}
	ackReplies := client.Handle(packetWithBody(sessionID, parsePacket.MessageId, &cpdv1.Packet_ParseCompleteAck{ParseCompleteAck: &cpdv1.ParseCompleteAck{
		Esn: cfg.ESN, DeviceTypes: cfg.DeviceTypes, Result: cpdv1.Result_RESULT_SUCCESS,
	}}), now.Add(5500*time.Millisecond))
	if len(ackReplies) != 0 || client.State() != StateTerminal {
		t.Fatalf("ACK state=%s replies=%v", client.State(), ackReplies)
	}
	if replies := client.Handle(packetWithBody(sessionID, testID(26), &cpdv1.Packet_TransferEndNty{TransferEndNty: &cpdv1.TransferEndNty{}}), now.Add(6*time.Second)); len(replies) != 0 {
		t.Fatalf("terminal client handled old message: %v", replies)
	}
	if replies := client.Handle(packetWithBody(sessionID, testID(31), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now.Add(6*time.Second)); len(replies) != 0 {
		t.Fatalf("terminal client replied to old-session discovery: %v", replies)
	}
	newSession := testID(32)
	if replies := client.Handle(packetWithBody(newSession, testID(33), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now.Add(6*time.Second)); len(replies) != 1 || client.State() != StateDiscovered {
		t.Fatalf("new discovery replies=%v state=%s", replies, client.State())
	}
}

func TestClientCacheHitStillParses(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	parseCalls := 0
	client := newTestClient(t, root, cfg, func(_ ParseRequest) ParseOutcome {
		parseCalls++
		return ParseOutcome{Result: cpdv1.Result_RESULT_FAILED, ErrorCode: cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE,
			TypeResults: []*cpdv1.ParseTypeResult{{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, DeviceId: "dc_ccu_1", Result: cpdv1.Result_RESULT_FAILED, Stage: cpdv1.ParseStage_PARSE_STAGE_VALIDATE, ErrorCode: cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE}}}
	})
	now := time.Unix(300, 0)
	sessionID := testID(30)
	authenticateOne(t, client, cfg, sessionID, now)
	data := []byte("cached")
	hash := sha256.Sum256(data)
	cacheDir := filepath.Join(root, "txbz")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "same.zip"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	start := &cpdv1.TransferStartNty{FileName: "same.zip", FileSize: uint64(len(data)), FileSha256: hash[:], ExpandedSize: 20, RequiredWorkspace: 30, ChunkSize: ChunkSize, TotalChunks: 1}
	replies := client.Handle(packetWithBody(sessionID, testID(33), &cpdv1.Packet_TransferStartNty{TransferStartNty: start}), now.Add(2*time.Second))
	if len(replies) != 1 || replies[0].GetTransferCompleteRsp().GetStage() != cpdv1.TransferStage_TRANSFER_STAGE_CACHE_REUSE {
		t.Fatalf("cache replies = %v", replies)
	}
	_ = waitForParsePacket(t, client, now.Add(2*time.Second))
	if parseCalls != 1 {
		t.Fatalf("cache hit parse calls = %d", parseCalls)
	}
}

func TestClientNewAuthPreemptsOldTransferAfterDiscovery(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	client := newTestClient(t, root, cfg, nil)
	now := time.Unix(400, 0)
	oldSession := testID(40)
	authenticateOne(t, client, cfg, oldSession, now)
	data := bytes.Repeat([]byte{1}, 1300)
	hash := sha256.Sum256(data)
	start := &cpdv1.TransferStartNty{FileName: "old.zip", FileSize: uint64(len(data)), FileSha256: hash[:], ExpandedSize: 2000, RequiredWorkspace: 4000, ChunkSize: ChunkSize, TotalChunks: 2}
	client.Handle(packetWithBody(oldSession, testID(43), &cpdv1.Packet_TransferStartNty{TransferStartNty: start}), now.Add(2*time.Second))
	if client.State() != StateReceiving {
		t.Fatalf("old state = %s", client.State())
	}

	newSession := testID(50)
	client.Handle(packetWithBody(newSession, testID(51), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now.Add(3*time.Second))
	replies := client.Handle(authPacket(newSession, testID(52), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_2", DeviceId: "dc_ccu_2"}), now.Add(4*time.Second))
	if len(replies) != 1 || replies[0].GetAuthRsp().GetResult() != cpdv1.Result_RESULT_SUCCESS || client.SessionID()[0] != newSession[0] {
		t.Fatalf("preempt replies=%v state=%s session=%x", replies, client.State(), client.SessionID())
	}
}

func TestClientWaitsForCompleteNewBindingBeforePreemption(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU", "TEDS"})
	client := newTestClient(t, root, cfg, nil)
	now := time.Unix(450, 0)
	oldSession := testID(53)
	client.Handle(packetWithBody(oldSession, testID(54), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now)
	client.Handle(authPacket(oldSession, testID(55), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_old", DeviceId: "dc_ccu_old"}), now.Add(time.Second))
	oldAuth := client.Handle(authPacket(oldSession, testID(56), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_SERVER, Esn: cfg.ESN, NodeId: "nn_old", DeviceId: "dc_server_old"}), now.Add(2*time.Second))
	if len(oldAuth) != 1 || oldAuth[0].GetAuthRsp().GetResult() != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("old auth = %v", oldAuth)
	}
	data := bytes.Repeat([]byte{1}, 1300)
	hash := sha256.Sum256(data)
	start := &cpdv1.TransferStartNty{FileName: "old.zip", FileSize: uint64(len(data)), FileSha256: hash[:], ExpandedSize: 2000, RequiredWorkspace: 4000, ChunkSize: ChunkSize, TotalChunks: 2}
	client.Handle(packetWithBody(oldSession, testID(57), &cpdv1.Packet_TransferStartNty{TransferStartNty: start}), now.Add(3*time.Second))

	newSession := testID(58)
	client.Handle(packetWithBody(newSession, testID(59), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now.Add(4*time.Second))
	partial := client.Handle(authPacket(newSession, testID(60), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_new", DeviceId: "dc_ccu_new"}), now.Add(5*time.Second))
	if len(partial) != 0 || client.State() != StateReceiving || !bytes.Equal(client.SessionID(), oldSession) {
		t.Fatalf("partial preempted old session: replies=%v state=%s session=%x", partial, client.State(), client.SessionID())
	}
	complete := client.Handle(authPacket(newSession, testID(61), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_SERVER, Esn: cfg.ESN, NodeId: "nn_new", DeviceId: "dc_server_new"}), now.Add(6*time.Second))
	if len(complete) != 1 || complete[0].GetAuthRsp().GetResult() != cpdv1.Result_RESULT_SUCCESS || client.State() != StateAuthenticated || !bytes.Equal(client.SessionID(), newSession) {
		t.Fatalf("complete takeover failed: replies=%v state=%s session=%x", complete, client.State(), client.SessionID())
	}
}

func TestClientParseTimeoutSendsExactlyFiveIdenticalResults(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	client := newTestClient(t, root, cfg, func(request ParseRequest) ParseOutcome {
		<-request.Context.Done()
		return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT)
	})
	now := time.Unix(500, 0)
	client.sessionID = testID(60)
	client.nodeID = "nn_1"
	client.bindings = map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_CCU: "dc_ccu_1"}
	client.start = &cpdv1.TransferStartNty{FileName: "txbz.zip", FileSha256: bytes.Repeat([]byte{1}, 32), ExpandedSize: 1}
	client.beginParse(now)

	first := client.Tick(now.Add(30 * time.Second))
	if len(first) != 1 || first[0].GetParseCompleteReq().GetErrorCode() != cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT {
		t.Fatalf("timeout result = %v", first)
	}
	all := []*cpdv1.Packet{first[0]}
	for second := 31; second <= 34; second++ {
		packets := client.Tick(now.Add(time.Duration(second) * time.Second))
		if len(packets) != 1 {
			t.Fatalf("second %d packets = %v", second, packets)
		}
		all = append(all, packets[0])
	}
	if packets := client.Tick(now.Add(35 * time.Second)); len(packets) != 0 || client.State() != StateTerminal {
		t.Fatalf("after fifth attempt state=%s packets=%v", client.State(), packets)
	}
	for _, packet := range all[1:] {
		if !proto.Equal(packet, all[0]) {
			t.Fatal("parse retry changed message ID or body")
		}
	}
}

func TestClientAuthenticationWindowExpiresWithoutBinding(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU", "TEDS"})
	client := newTestClient(t, root, cfg, nil)
	now := time.Unix(600, 0)
	sessionID := testID(70)
	client.Handle(packetWithBody(sessionID, testID(71), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now)
	client.Handle(authPacket(sessionID, testID(72), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_1", DeviceId: "dc_ccu_1"}), now.Add(time.Second))
	if client.State() != StateAuthenticating {
		t.Fatalf("state = %s", client.State())
	}
	client.Tick(now.Add(time.Second + 5*time.Second))
	if client.State() != StateTerminal {
		t.Fatalf("auth timeout state = %s", client.State())
	}
}

func TestClientRejectsLateConflictingAuthInSameSession(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	client := newTestClient(t, root, cfg, nil)
	now := time.Unix(700, 0)
	sessionID := testID(75)
	authenticateOne(t, client, cfg, sessionID, now)
	replies := client.Handle(authPacket(sessionID, testID(76), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_1", DeviceId: "dc_ccu_conflict"}), now.Add(2*time.Second))
	if len(replies) != 1 || replies[0].GetAuthRsp().GetResult() != cpdv1.Result_RESULT_FAILED || replies[0].GetAuthRsp().GetErrorCode() != cpdv1.ErrorCode_ERROR_CODE_AUTH_CONFLICT {
		t.Fatalf("conflict replies = %v", replies)
	}
	if client.State() != StateTerminal {
		t.Fatalf("state = %s", client.State())
	}
}

func TestClientRejectsTransferStartBeforeAuthentication(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	client := newTestClient(t, root, cfg, nil)
	now := time.Unix(800, 0)
	sessionID := testID(77)
	client.Handle(packetWithBody(sessionID, testID(78), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now)
	data := []byte("zip")
	hash := sha256.Sum256(data)
	start := &cpdv1.TransferStartNty{FileName: "txbz.zip", FileSize: 3, FileSha256: hash[:], ExpandedSize: 10, RequiredWorkspace: 20, ChunkSize: ChunkSize, TotalChunks: 1}
	replies := client.Handle(packetWithBody(sessionID, testID(79), &cpdv1.Packet_TransferStartNty{TransferStartNty: start}), now.Add(time.Second))
	if len(replies) != 1 || replies[0].GetTransferCompleteRsp().GetErrorCode() != cpdv1.ErrorCode_ERROR_CODE_AUTH_BINDING_MISSING {
		t.Fatalf("replies = %v", replies)
	}
	if client.State() != StateTerminal {
		t.Fatalf("state = %s", client.State())
	}
}

func TestClientReturnsBusyWhenOldFormalWriteDoesNotStopInAuthWindow(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	release := make(chan struct{})
	client := newTestClient(t, root, cfg, func(request ParseRequest) ParseOutcome {
		<-release
		return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT)
	})
	now := time.Unix(900, 0)
	client.sessionID = testID(90)
	client.nodeID = "nn_old"
	client.bindings = map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_CCU: "dc_ccu_old"}
	client.start = &cpdv1.TransferStartNty{FileName: "old.zip", FileSha256: bytes.Repeat([]byte{1}, 32), ExpandedSize: 1}
	client.beginParse(now)

	newSession := testID(91)
	client.Handle(packetWithBody(newSession, testID(92), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now.Add(time.Second))
	immediate := client.Handle(authPacket(newSession, testID(93), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_new", DeviceId: "dc_ccu_new"}), now.Add(2*time.Second))
	if len(immediate) != 0 {
		t.Fatalf("takeover replied before old write stopped: %v", immediate)
	}
	replies := client.Tick(now.Add(7 * time.Second))
	if len(replies) != 1 || replies[0].GetAuthRsp().GetResult() != cpdv1.Result_RESULT_FAILED || replies[0].GetAuthRsp().GetErrorCode() != cpdv1.ErrorCode_ERROR_CODE_BUSY {
		t.Fatalf("busy replies = %v", replies)
	}
	if client.State() != StateTerminal || !bytes.Equal(client.SessionID(), newSession) {
		t.Fatalf("state=%s session=%x", client.State(), client.SessionID())
	}
	close(release)
}

func TestClientActivatesNewSessionAfterOldParseStops(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	client := newTestClient(t, root, cfg, func(request ParseRequest) ParseOutcome {
		<-request.Context.Done()
		return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT)
	})
	now := time.Unix(1_000, 0)
	client.sessionID = testID(100)
	client.nodeID = "nn_old"
	client.bindings = map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_CCU: "dc_ccu_old"}
	client.start = &cpdv1.TransferStartNty{FileName: "old.zip", FileSha256: bytes.Repeat([]byte{1}, 32), ExpandedSize: 1}
	client.beginParse(now)

	newSession := testID(101)
	client.Handle(packetWithBody(newSession, testID(102), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now.Add(time.Second))
	if replies := client.Handle(authPacket(newSession, testID(103), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_new", DeviceId: "dc_ccu_new"}), now.Add(2*time.Second)); len(replies) != 0 {
		t.Fatalf("takeover replied before cancellation completed: %v", replies)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		replies := client.Tick(now.Add(3 * time.Second))
		if len(replies) == 1 {
			if replies[0].GetAuthRsp().GetResult() != cpdv1.Result_RESULT_SUCCESS {
				t.Fatalf("auth result = %v", replies[0].GetAuthRsp())
			}
			if client.State() != StateAuthenticated || !bytes.Equal(client.SessionID(), newSession) {
				t.Fatalf("state=%s session=%x", client.State(), client.SessionID())
			}
			return
		}
		runtime.Gosched()
	}
	t.Fatal("timed out waiting for old parse cancellation")
}

func testConfig(t *testing.T, types []string) (string, *config.Config) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "install", "cpdc")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	value := map[string]any{
		"deviceTypes": types, "esn": "123456789012345678901234567890123456789",
		"txbzName": "", "txbzHash": "", "nodeId": "", "deviceIds": map[string]string{},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, config.FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return root, cfg
}

func newTestClient(t *testing.T, root string, cfg *config.Config, parse ParseFunc) *Client {
	t.Helper()
	if parse == nil {
		parse = func(request ParseRequest) ParseOutcome {
			results := make([]*cpdv1.ParseTypeResult, len(request.Config.DeviceTypes))
			for i, deviceType := range request.Config.DeviceTypes {
				results[i] = &cpdv1.ParseTypeResult{DeviceType: deviceType, DeviceId: request.Bindings[deviceType], Result: cpdv1.Result_RESULT_SUCCESS}
			}
			return ParseOutcome{Result: cpdv1.Result_RESULT_SUCCESS, TypeResults: results}
		}
	}
	counter := byte(80)
	client, err := New(Options{
		Root: root, Config: cfg, IP: "10.0.0.2", SubnetMask: "255.255.255.0",
		InstanceNonce: bytes.Repeat([]byte{9}, 16), WorkspaceFactor: 1,
		AvailableSpace: func(string) (uint64, error) { return math.MaxUint64, nil },
		NewID:          func() ([]byte, error) { counter++; return testID(counter), nil },
		Parse:          parse,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func authenticateOne(t *testing.T, client *Client, cfg *config.Config, sessionID []byte, now time.Time) {
	t.Helper()
	client.Handle(packetWithBody(sessionID, testID(21), &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}}), now)
	replies := client.Handle(authPacket(sessionID, testID(22), cfg.ESN,
		&cpdv1.DeviceAssignment{DeviceType: cpdv1.DeviceType_DEVICE_TYPE_CCU, Esn: cfg.ESN, NodeId: "nn_1", DeviceId: "dc_ccu_1"}), now.Add(time.Second))
	if len(replies) != 1 || replies[0].GetAuthRsp().GetResult() != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("authentication failed: %v", replies)
	}
}

func authPacket(sessionID, messageID []byte, _ string, assignments ...*cpdv1.DeviceAssignment) *cpdv1.Packet {
	return packetWithBody(sessionID, messageID, &cpdv1.Packet_AuthNty{AuthNty: &cpdv1.AuthNty{Assignments: assignments}})
}

func packetWithBody(sessionID, messageID []byte, body any) *cpdv1.Packet {
	packet := &cpdv1.Packet{SessionId: sessionID, MessageId: messageID}
	switch typed := body.(type) {
	case *cpdv1.Packet_DiscoverNty:
		packet.Body = typed
	case *cpdv1.Packet_AuthNty:
		packet.Body = typed
	case *cpdv1.Packet_TransferStartNty:
		packet.Body = typed
	case *cpdv1.Packet_TransferChunkNty:
		packet.Body = typed
	case *cpdv1.Packet_TransferEndNty:
		packet.Body = typed
	case *cpdv1.Packet_ParseCompleteAck:
		packet.Body = typed
	default:
		panic("unsupported test packet body")
	}
	return packet
}

func waitForParsePacket(t *testing.T, client *Client, now time.Time) *cpdv1.Packet {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		packets := client.Tick(now)
		if len(packets) > 0 && packets[0].GetParseCompleteReq() != nil {
			return packets[0]
		}
		runtime.Gosched()
	}
	t.Fatal("timed out waiting for parse result")
	return nil
}

func crc(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}
