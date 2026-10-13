package client

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/config"
	"cpdc/internal/fsutil"
	"cpdc/internal/protocol"
	"google.golang.org/protobuf/proto"
)

type State string

const (
	StateIdle           State = "idle"
	StateDiscovered     State = "discovered"
	StateAuthenticating State = "authenticating"
	StateAuthenticated  State = "authenticated"
	StateReceiving      State = "receiving"
	StateParsing        State = "parsing"
	StateReporting      State = "reporting"
	StateTerminal       State = "terminal"
)

type Options struct {
	Root            string
	Config          *config.Config
	TarPath         string
	ExecutableRoot  string
	IP              string
	SubnetMask      string
	InstanceNonce   []byte
	WorkspaceFactor uint64
	AvailableSpace  func(string) (uint64, error)
	NewID           IDFactory
	Parse           ParseFunc
}

type parseEvent struct {
	generation uint64
	outcome    ParseOutcome
}

type pendingTakeover struct {
	sessionID []byte
	auth      *AuthAccumulator
	decision  AuthDecision
	messageID []byte
	deadline  time.Time
}

type Client struct {
	root          string
	workspacePath string
	exeRoot       string
	config        *config.Config
	ip            string
	subnetMask    string
	instanceNonce []byte
	factor        uint64
	available     func(string) (uint64, error)
	newID         IDFactory
	parse         ParseFunc

	state       State
	recent      map[string]time.Time
	sessionID   []byte
	auth        *AuthAccumulator
	authUntil   time.Time
	nodeID      string
	bindings    map[cpdv1.DeviceType]string
	lastActive  time.Time
	start       *cpdv1.TransferStartNty
	transfer    *Transfer
	packagePath string

	transferComplete      bool
	transferCompleteStage cpdv1.TransferStage

	generation    uint64
	parseCancel   context.CancelFunc
	parseDeadline time.Time
	parseEvents   chan parseEvent
	parseActive   bool
	parseActiveID uint64

	report         *cpdv1.Packet
	reportAttempts int
	nextReport     time.Time

	pendingSession []byte
	pendingAuth    *AuthAccumulator
	pendingUntil   time.Time
	takeover       *pendingTakeover
}

func New(options Options) (*Client, error) {
	if options.Root == "" || options.Config == nil || options.IP == "" || options.SubnetMask == "" || len(options.InstanceNonce) != 16 {
		return nil, errors.New("incomplete client options")
	}
	if options.WorkspaceFactor == 0 {
		options.WorkspaceFactor = 1
	}
	if options.AvailableSpace == nil {
		options.AvailableSpace = fsutil.AvailableSpace
	}
	if options.NewID == nil {
		options.NewID = protocol.NewRandomID
	}
	if options.Parse == nil {
		options.Parse = NewParser().Parse
	}
	workspacePath := options.Root
	if options.TarPath != "" {
		workspacePath = options.TarPath
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			return nil, fmt.Errorf("create tar path %s: %w", workspacePath, err)
		}
	}
	return &Client{
		root: options.Root, workspacePath: workspacePath, exeRoot: options.ExecutableRoot, config: options.Config, ip: options.IP, subnetMask: options.SubnetMask,
		instanceNonce: append([]byte(nil), options.InstanceNonce...), factor: options.WorkspaceFactor,
		available: options.AvailableSpace, newID: options.NewID, parse: options.Parse,
		state: StateIdle, recent: make(map[string]time.Time), bindings: make(map[cpdv1.DeviceType]string),
		parseEvents: make(chan parseEvent, 8),
	}, nil
}

func (c *Client) State() State { return c.state }

func (c *Client) SessionID() []byte { return protocol.CloneID(c.sessionID) }

// SetDisplayAddress updates the IP and subnet mask reported in DISCOVER_RSP.
func (c *Client) SetDisplayAddress(ip, subnetMask string) {
	c.ip = ip
	c.subnetMask = subnetMask
}

func (c *Client) Handle(packet *cpdv1.Packet, now time.Time) []*cpdv1.Packet {
	if packet == nil || !protocol.ValidID(packet.GetSessionId()) || !protocol.ValidID(packet.GetMessageId()) || packet.GetBody() == nil {
		return nil
	}
	if packet.GetDiscoverNty() != nil {
		return c.handleDiscover(packet, now)
	}
	if c.state == StateReporting {
		if packet.GetParseCompleteAck() != nil {
			c.handleParseAck(packet)
		}
		return nil
	}
	if c.state == StateTerminal {
		return nil
	}
	switch {
	case packet.GetAuthNty() != nil:
		return c.handleAuth(packet, now)
	case packet.GetTransferStartNty() != nil:
		return c.handleStart(packet, now)
	case packet.GetTransferChunkNty() != nil:
		return c.handleChunk(packet, now)
	case packet.GetTransferEndNty() != nil:
		return c.handleEnd(packet, now)
	case packet.GetParseCompleteAck() != nil:
		c.handleParseAck(packet)
	}
	return nil
}

func (c *Client) Tick(now time.Time) []*cpdv1.Packet {
	for key, discoveredAt := range c.recent {
		if now.Sub(discoveredAt) > protocol.RecentDiscoverValidity {
			delete(c.recent, key)
		}
	}
	var replies []*cpdv1.Packet
drain:
	for {
		select {
		case event := <-c.parseEvents:
			if event.generation == c.parseActiveID {
				c.parseActive = false
			}
			if c.takeover != nil && !c.parseActive {
				replies = append(replies, c.activateTakeover(now))
				continue
			}
			if event.generation == c.generation && c.state == StateParsing {
				if packet := c.finishParse(event.outcome, now); packet != nil {
					replies = append(replies, packet)
				}
			}
		default:
			break drain
		}
	}
	if c.takeover != nil {
		if !c.parseActive {
			replies = append(replies, c.activateTakeover(now))
		} else if !now.Before(c.takeover.deadline) {
			replies = append(replies, c.failBusyTakeover())
		}
		return replies
	}
	if c.state == StateAuthenticating && !c.authUntil.IsZero() && !now.Before(c.authUntil) {
		c.finishTerminal()
		return replies
	}
	if c.pendingAuth != nil && !c.pendingUntil.IsZero() && !now.Before(c.pendingUntil) {
		c.clearPendingAuth()
	}
	if (c.state == StateAuthenticated || c.state == StateReceiving) && !c.lastActive.IsZero() && now.Sub(c.lastActive) >= protocol.ClientSessionIdle {
		c.finishTerminal()
		return replies
	}
	if c.state == StateParsing && !c.parseDeadline.IsZero() && !now.Before(c.parseDeadline) {
		if c.parseCancel != nil {
			c.parseCancel()
		}
		outcome := globalParseFailure(ParseRequest{Config: c.config, Bindings: c.bindings}, cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT)
		if packet := c.finishParse(outcome, now); packet != nil {
			replies = append(replies, packet)
		}
	}
	if c.state == StateReporting && c.report != nil && !now.Before(c.nextReport) {
		if c.reportAttempts < protocol.ParseResultMaxAttempts {
			replies = append(replies, proto.Clone(c.report).(*cpdv1.Packet))
			c.reportAttempts++
			c.nextReport = now.Add(protocol.ParseResultRetry)
		} else {
			c.finishTerminal()
		}
	}
	return replies
}

func (c *Client) handleDiscover(packet *cpdv1.Packet, now time.Time) []*cpdv1.Packet {
	if (c.state == StateTerminal || c.state == StateReporting) && c.sameSession(packet) {
		return nil
	}
	if c.state == StateTerminal || c.state == StateReporting {
		c.resetCurrent()
	}
	c.recent[sessionKey(packet.GetSessionId())] = now
	if c.state == StateIdle {
		c.state = StateDiscovered
	}
	reply := &cpdv1.Packet{
		SessionId: protocol.CloneID(packet.GetSessionId()), MessageId: protocol.CloneID(packet.GetMessageId()),
		Body: &cpdv1.Packet_DiscoverRsp{DiscoverRsp: &cpdv1.DiscoverRsp{
			Esn: c.config.ESN, InstanceNonce: append([]byte(nil), c.instanceNonce...),
			DeviceTypes: append([]cpdv1.DeviceType(nil), c.config.DeviceTypes...), CurrentIp: c.ip, SubnetMask: c.subnetMask,
		}},
	}
	return []*cpdv1.Packet{reply}
}

func (c *Client) handleAuth(packet *cpdv1.Packet, now time.Time) []*cpdv1.Packet {
	discoveredAt, ok := c.recent[sessionKey(packet.GetSessionId())]
	if !ok || now.Sub(discoveredAt) > protocol.RecentDiscoverValidity {
		return nil
	}
	if c.takeover != nil {
		return nil
	}
	if len(c.sessionID) != 0 && !protocol.SameID(c.sessionID, packet.GetSessionId()) {
		return c.handlePendingAuth(packet, now)
	}
	if len(c.sessionID) == 0 || !protocol.SameID(c.sessionID, packet.GetSessionId()) {
		c.resetCurrent()
		c.sessionID = protocol.CloneID(packet.GetSessionId())
		c.auth = NewAuthAccumulator(c.config.ESN, c.config.DeviceTypes)
		c.authUntil = now.Add(protocol.AuthWindow)
		c.state = StateAuthenticating
	} else if c.state == StateAuthenticated || c.state == StateReceiving || c.state == StateParsing || c.state == StateReporting {
		if c.auth == nil {
			return nil
		}
		decision := c.auth.Apply(packet.GetAuthNty())
		if !decision.Done {
			return nil
		}
		reply := c.authResponse(packet.GetMessageId(), decision)
		if decision.Result == cpdv1.Result_RESULT_FAILED {
			c.finishTerminal()
		}
		return []*cpdv1.Packet{reply}
	}
	if c.auth == nil {
		c.auth = NewAuthAccumulator(c.config.ESN, c.config.DeviceTypes)
		c.authUntil = now.Add(protocol.AuthWindow)
		c.state = StateAuthenticating
	}
	decision := c.auth.Apply(packet.GetAuthNty())
	c.lastActive = now
	if !decision.Done {
		return nil
	}
	reply := c.authResponse(packet.GetMessageId(), decision)
	if decision.Result == cpdv1.Result_RESULT_FAILED {
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	if c.parseActive {
		c.queueTakeover(c.sessionID, c.auth, decision, packet.GetMessageId(), c.authUntil)
		return nil
	}
	c.nodeID = decision.NodeID
	c.bindings = c.auth.BindingMap()
	c.state = StateAuthenticated
	return []*cpdv1.Packet{reply}
}

func (c *Client) handlePendingAuth(packet *cpdv1.Packet, now time.Time) []*cpdv1.Packet {
	if c.pendingAuth == nil || !protocol.SameID(c.pendingSession, packet.GetSessionId()) {
		c.pendingSession = protocol.CloneID(packet.GetSessionId())
		c.pendingAuth = NewAuthAccumulator(c.config.ESN, c.config.DeviceTypes)
		c.pendingUntil = now.Add(protocol.AuthWindow)
	}
	if !now.Before(c.pendingUntil) {
		c.clearPendingAuth()
		return nil
	}
	decision := c.pendingAuth.Apply(packet.GetAuthNty())
	if !decision.Done {
		return nil
	}
	if decision.Result == cpdv1.Result_RESULT_SUCCESS && c.parseActive {
		c.queueTakeover(c.pendingSession, c.pendingAuth, decision, packet.GetMessageId(), c.pendingUntil)
		return nil
	}
	pendingSession := protocol.CloneID(c.pendingSession)
	pendingAuth := c.pendingAuth
	c.resetCurrent()
	c.sessionID = pendingSession
	c.auth = pendingAuth
	reply := c.authResponse(packet.GetMessageId(), decision)
	if decision.Result == cpdv1.Result_RESULT_FAILED {
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	c.nodeID = decision.NodeID
	c.bindings = c.auth.BindingMap()
	c.lastActive = now
	c.state = StateAuthenticated
	return []*cpdv1.Packet{reply}
}

func (c *Client) authResponse(messageID []byte, decision AuthDecision) *cpdv1.Packet {
	return &cpdv1.Packet{
		SessionId: protocol.CloneID(c.sessionID), MessageId: protocol.CloneID(messageID),
		Body: &cpdv1.Packet_AuthRsp{AuthRsp: &cpdv1.AuthRsp{
			Client: c.identity(), Result: decision.Result, NodeId: decision.NodeID,
			Bindings: cloneBindings(decision.Bindings), ErrorCode: decision.ErrorCode, Conflict: decision.Conflict,
		}},
	}
}

func (c *Client) handleStart(packet *cpdv1.Packet, now time.Time) []*cpdv1.Packet {
	if !c.sameSession(packet) {
		discoveredAt, discovered := c.recent[sessionKey(packet.GetSessionId())]
		if !discovered || now.Sub(discoveredAt) > protocol.RecentDiscoverValidity || (c.state != StateIdle && c.state != StateDiscovered) {
			return nil
		}
		c.resetCurrent()
		c.sessionID = protocol.CloneID(packet.GetSessionId())
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_PRECHECK, 0, cpdv1.ErrorCode_ERROR_CODE_AUTH_BINDING_MISSING)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	start := packet.GetTransferStartNty()
	if (c.state == StateParsing || c.state == StateReporting) && c.transferComplete && proto.Equal(c.start, start) {
		return []*cpdv1.Packet{c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_SUCCESS, c.transferCompleteStage, c.totalChunks(), cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED)}
	}
	if c.state == StateReceiving {
		c.lastActive = now
		if proto.Equal(c.start, start) {
			return nil
		}
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_PRECHECK, c.receivedChunks(), cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	if c.state == StateAuthenticating {
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_PRECHECK, 0, cpdv1.ErrorCode_ERROR_CODE_AUTH_BINDING_MISSING)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	if c.state != StateAuthenticated {
		return nil
	}
	available, err := c.available(c.workspacePath)
	if err != nil {
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_PRECHECK, 0, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	code := ValidateTransferStart(start, true, available)
	if code == cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		if start.GetRequiredWorkspace() > math.MaxUint64/c.factor || available < start.GetRequiredWorkspace()*c.factor {
			code = cpdv1.ErrorCode_ERROR_CODE_INSUFFICIENT_STORAGE
		}
	}
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_PRECHECK, 0, code)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	c.start = proto.Clone(start).(*cpdv1.TransferStartNty)
	c.lastActive = now
	hit, cachedPath, err := CacheMatch(c.root, start)
	if err != nil {
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_PRECHECK, 0, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	if hit {
		if err := PruneCache(c.root, cachedPath); err != nil {
			reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_PRECHECK, 0, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
			c.finishTerminal()
			return []*cpdv1.Packet{reply}
		}
		c.packagePath = cachedPath
		c.transferComplete = true
		c.transferCompleteStage = cpdv1.TransferStage_TRANSFER_STAGE_CACHE_REUSE
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_SUCCESS, c.transferCompleteStage, start.GetTotalChunks(), cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED)
		c.beginParse(now)
		return []*cpdv1.Packet{reply}
	}
	c.transfer, err = OpenTransfer(c.root, c.sessionID, start)
	if err != nil {
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_PRECHECK, 0, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	c.state = StateReceiving
	return nil
}

func (c *Client) handleChunk(packet *cpdv1.Packet, now time.Time) []*cpdv1.Packet {
	if c.state != StateReceiving || !c.sameSession(packet) || c.transfer == nil {
		return nil
	}
	chunk := packet.GetTransferChunkNty()
	progress, accepted, err := c.transfer.Accept(chunk.GetChunkIndex(), chunk.GetPayload(), chunk.GetPayloadCrc32())
	if err != nil {
		if errors.Is(err, ErrInvalidChunk) {
			return nil
		}
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_RECEIVE, c.receivedChunks(), cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	if !accepted {
		return nil
	}
	c.lastActive = now
	if progress == 0 {
		return nil
	}
	return []*cpdv1.Packet{{
		SessionId: protocol.CloneID(c.sessionID), MessageId: protocol.CloneID(packet.GetMessageId()),
		Body: &cpdv1.Packet_TransferProgressRsp{TransferProgressRsp: &cpdv1.TransferProgressRsp{
			Client: c.identity(), ReceivedChunks: c.transfer.ReceivedChunks(), TotalChunks: c.transfer.TotalChunks(), Percent: progress,
		}},
	}}
}

func (c *Client) handleEnd(packet *cpdv1.Packet, now time.Time) []*cpdv1.Packet {
	if !c.sameSession(packet) {
		return nil
	}
	if c.state != StateReceiving || c.transfer == nil {
		return nil
	}
	c.lastActive = now
	missing := c.transfer.MissingRanges()
	if len(missing) != 0 {
		packets, err := BuildLossPackets(c.sessionID, c.identity(), missing, c.newID)
		if err != nil {
			reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_RECEIVE, c.receivedChunks(), cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
			c.finishTerminal()
			return []*cpdv1.Packet{reply}
		}
		return packets
	}
	temporary, code, err := c.transfer.Finalize()
	if err != nil {
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_VERIFY, c.receivedChunks(), code)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	c.packagePath, err = CommitCache(c.root, temporary, c.start)
	if err != nil {
		reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_FAILED, cpdv1.TransferStage_TRANSFER_STAGE_VERIFY, c.receivedChunks(), cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
		c.finishTerminal()
		return []*cpdv1.Packet{reply}
	}
	_ = c.transfer.Abort()
	c.transfer = nil
	c.transferComplete = true
	c.transferCompleteStage = cpdv1.TransferStage_TRANSFER_STAGE_VERIFY
	reply := c.transferResult(packet.GetMessageId(), cpdv1.Result_RESULT_SUCCESS, c.transferCompleteStage, c.start.GetTotalChunks(), cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED)
	c.beginParse(now)
	return []*cpdv1.Packet{reply}
}

func (c *Client) transferResult(messageID []byte, result cpdv1.Result, stage cpdv1.TransferStage, received uint32, code cpdv1.ErrorCode) *cpdv1.Packet {
	return &cpdv1.Packet{
		SessionId: protocol.CloneID(c.sessionID), MessageId: protocol.CloneID(messageID),
		Body: &cpdv1.Packet_TransferCompleteRsp{TransferCompleteRsp: &cpdv1.TransferCompleteRsp{
			Client: c.identity(), Result: result, Stage: stage, ReceivedChunks: received, ErrorCode: code,
		}},
	}
}

func (c *Client) beginParse(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), protocol.ParseTimeout)
	c.parseCancel = cancel
	c.parseDeadline = now.Add(protocol.ParseTimeout)
	c.state = StateParsing
	generation := c.generation
	c.parseActive = true
	c.parseActiveID = generation
	request := ParseRequest{
		Context: ctx, Root: c.root, WorkspacePath: c.workspacePath, ExecutableRoot: c.exeRoot, Config: c.config, PackagePath: c.packagePath,
		PackageName: c.start.GetFileName(), FileHash: append([]byte(nil), c.start.GetFileSha256()...),
		ExpectedExpandedSize: c.start.GetExpandedSize(), RequiredWorkspace: c.start.GetRequiredWorkspace() * c.factor,
		AvailableSpace: c.available, NodeID: c.nodeID, Bindings: cloneBindingMap(c.bindings),
	}
	go func() {
		outcome := c.parse(request)
		c.parseEvents <- parseEvent{generation: generation, outcome: outcome}
	}()
}

func (c *Client) finishParse(outcome ParseOutcome, now time.Time) *cpdv1.Packet {
	if c.parseCancel != nil {
		c.parseCancel()
		c.parseCancel = nil
	}
	messageID, err := c.newID()
	if err != nil || !protocol.ValidID(messageID) {
		c.finishTerminal()
		return nil
	}
	packet := &cpdv1.Packet{
		SessionId: protocol.CloneID(c.sessionID), MessageId: protocol.CloneID(messageID),
		Body: &cpdv1.Packet_ParseCompleteReq{ParseCompleteReq: &cpdv1.ParseCompleteReq{
			Client: c.identity(), Result: outcome.Result, NodeId: c.nodeID, Bindings: c.bindingList(),
			TypeResults: cloneTypeResults(outcome.TypeResults), ErrorCode: outcome.ErrorCode,
		}},
	}
	c.report = packet
	c.reportAttempts = 1
	c.nextReport = now.Add(protocol.ParseResultRetry)
	c.state = StateReporting
	return proto.Clone(packet).(*cpdv1.Packet)
}

func (c *Client) handleParseAck(packet *cpdv1.Packet) {
	if c.state != StateReporting || c.report == nil || !c.sameSession(packet) || !protocol.SameID(packet.GetMessageId(), c.report.GetMessageId()) {
		return
	}
	ack := packet.GetParseCompleteAck()
	request := c.report.GetParseCompleteReq()
	if ack.GetEsn() != c.config.ESN || ack.GetResult() != request.GetResult() || !sameDeviceTypeSet(ack.GetDeviceTypes(), c.config.DeviceTypes) {
		return
	}
	c.finishTerminal()
}

func (c *Client) identity() *cpdv1.ClientIdentity { return c.config.Identity() }

func (c *Client) sameSession(packet *cpdv1.Packet) bool {
	return len(c.sessionID) != 0 && protocol.SameID(c.sessionID, packet.GetSessionId())
}

func (c *Client) bindingList() []*cpdv1.DeviceBinding {
	bindings := make([]*cpdv1.DeviceBinding, 0, len(c.config.DeviceTypes))
	for _, deviceType := range c.config.DeviceTypes {
		if deviceID := c.bindings[deviceType]; deviceID != "" {
			bindings = append(bindings, &cpdv1.DeviceBinding{DeviceType: deviceType, DeviceId: deviceID})
		}
	}
	return bindings
}

func (c *Client) receivedChunks() uint32 {
	if c.transfer == nil {
		return 0
	}
	return c.transfer.ReceivedChunks()
}

func (c *Client) totalChunks() uint32 {
	if c.start == nil {
		return 0
	}
	return c.start.GetTotalChunks()
}

func (c *Client) resetCurrent() {
	if c.parseCancel != nil {
		c.parseCancel()
		c.parseCancel = nil
	}
	if c.transfer != nil {
		_ = c.transfer.Abort()
		c.transfer = nil
	}
	c.generation++
	c.sessionID = nil
	c.auth = nil
	c.authUntil = time.Time{}
	c.nodeID = ""
	c.bindings = make(map[cpdv1.DeviceType]string)
	c.lastActive = time.Time{}
	c.start = nil
	c.packagePath = ""
	c.transferComplete = false
	c.transferCompleteStage = cpdv1.TransferStage_TRANSFER_STAGE_UNSPECIFIED
	c.parseDeadline = time.Time{}
	c.report = nil
	c.reportAttempts = 0
	c.nextReport = time.Time{}
	c.clearPendingAuth()
	c.takeover = nil
	c.state = StateIdle
}

func (c *Client) finishTerminal() {
	if c.parseCancel != nil {
		c.parseCancel()
		c.parseCancel = nil
	}
	if c.transfer != nil {
		_ = c.transfer.Abort()
		c.transfer = nil
	}
	c.generation++
	c.state = StateTerminal
}

func sessionKey(sessionID []byte) string { return hex.EncodeToString(sessionID) }

func (c *Client) clearPendingAuth() {
	c.pendingSession = nil
	c.pendingAuth = nil
	c.pendingUntil = time.Time{}
}

func (c *Client) queueTakeover(sessionID []byte, auth *AuthAccumulator, decision AuthDecision, messageID []byte, deadline time.Time) {
	if c.parseCancel != nil {
		c.parseCancel()
	}
	c.takeover = &pendingTakeover{
		sessionID: protocol.CloneID(sessionID),
		auth:      auth,
		decision:  decision,
		messageID: protocol.CloneID(messageID),
		deadline:  deadline,
	}
	c.clearPendingAuth()
}

func (c *Client) activateTakeover(now time.Time) *cpdv1.Packet {
	pending := c.takeover
	c.resetCurrent()
	c.sessionID = protocol.CloneID(pending.sessionID)
	c.auth = pending.auth
	c.nodeID = pending.decision.NodeID
	c.bindings = c.auth.BindingMap()
	c.lastActive = now
	c.state = StateAuthenticated
	return c.authResponse(pending.messageID, pending.decision)
}

func (c *Client) failBusyTakeover() *cpdv1.Packet {
	pending := c.takeover
	c.resetCurrent()
	c.sessionID = protocol.CloneID(pending.sessionID)
	c.auth = pending.auth
	decision := pending.decision
	decision.Result = cpdv1.Result_RESULT_FAILED
	decision.ErrorCode = cpdv1.ErrorCode_ERROR_CODE_BUSY
	reply := c.authResponse(pending.messageID, decision)
	c.finishTerminal()
	return reply
}

func cloneBindingMap(source map[cpdv1.DeviceType]string) map[cpdv1.DeviceType]string {
	result := make(map[cpdv1.DeviceType]string, len(source))
	for deviceType, deviceID := range source {
		result[deviceType] = deviceID
	}
	return result
}

func cloneBindings(source []*cpdv1.DeviceBinding) []*cpdv1.DeviceBinding {
	result := make([]*cpdv1.DeviceBinding, len(source))
	for i, binding := range source {
		result[i] = proto.Clone(binding).(*cpdv1.DeviceBinding)
	}
	return result
}

func cloneTypeResults(source []*cpdv1.ParseTypeResult) []*cpdv1.ParseTypeResult {
	result := make([]*cpdv1.ParseTypeResult, len(source))
	for i, item := range source {
		result[i] = proto.Clone(item).(*cpdv1.ParseTypeResult)
	}
	return result
}

func sameDeviceTypeSet(a, b []cpdv1.DeviceType) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[cpdv1.DeviceType]int, len(a))
	for _, item := range a {
		counts[item]++
	}
	for _, item := range b {
		counts[item]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
