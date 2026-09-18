package client

import (
	"context"
	"encoding/hex"
	"errors"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/config"
	"cpdc/internal/fsutil"
	"cpdc/internal/output"
	"cpdc/internal/packagezip"
)

type ParseRequest struct {
	Context              context.Context
	Root                 string
	WorkspacePath        string
	ExecutableRoot       string
	Config               *config.Config
	PackagePath          string
	PackageName          string
	FileHash             []byte
	ExpectedExpandedSize uint64
	RequiredWorkspace    uint64
	AvailableSpace       func(string) (uint64, error)
	NodeID               string
	Bindings             map[cpdv1.DeviceType]string
}

type ParseOutcome struct {
	Result      cpdv1.Result
	TypeResults []*cpdv1.ParseTypeResult
	ErrorCode   cpdv1.ErrorCode
}

type ParseFunc func(ParseRequest) ParseOutcome

// Parser serializes parsing so a cancelled old session cannot race a new
// session while a single formal file replacement is finishing.
type Parser struct {
	gate            chan struct{}
	syncFilesystems func()
}

func NewParser() *Parser {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Parser{gate: gate, syncFilesystems: fsutil.SyncFilesystems}
}

func (p *Parser) Parse(request ParseRequest) ParseOutcome {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT)
	case <-p.gate:
	}
	defer func() { p.gate <- struct{}{} }()
	return parsePackage(ctx, request, p.syncFilesystems)
}

func parsePackage(ctx context.Context, request ParseRequest, syncFilesystems func()) ParseOutcome {
	if request.RequiredWorkspace != 0 {
		availableSpace := request.AvailableSpace
		if availableSpace == nil {
			availableSpace = fsutil.AvailableSpace
		}
		workspacePath := request.WorkspacePath
		if workspacePath == "" {
			workspacePath = request.Root
		}
		available, err := availableSpace(workspacePath)
		if err != nil {
			return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_VALIDATE, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
		}
		if available < request.RequiredWorkspace {
			return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_VALIDATE, cpdv1.ErrorCode_ERROR_CODE_INSUFFICIENT_STORAGE)
		}
	}
	pkg, err := packagezip.Validate(ctx, request.PackagePath)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT)
		}
		code := cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE
		if packageError, ok := packagezip.AsError(err); ok {
			code = packageError.Code
		}
		return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_VALIDATE, code)
	}
	defer pkg.Close()
	if request.ExpectedExpandedSize != 0 && pkg.ExpandedSize != request.ExpectedExpandedSize {
		return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_VALIDATE, cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE)
	}
	typeResults, code := output.Process(ctx, output.Request{
		Root: request.Root, Package: pkg, PackageName: request.PackageName, NodeID: request.NodeID,
		DeviceTypes: request.Config.DeviceTypes, Bindings: request.Bindings,
		ReReadjsonPath: request.Config.ReReadjsonPath,
		TarPath:        request.Config.TarPath,
		DefaultTarRoot: request.ExecutableRoot,
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		return ParseOutcome{Result: cpdv1.Result_RESULT_FAILED, TypeResults: typeResults, ErrorCode: code}
	}
	if err := ctx.Err(); err != nil {
		return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT)
	}
	deviceIDs := make(map[string]string, len(request.Bindings))
	for deviceType, deviceID := range request.Bindings {
		deviceIDs[config.DeviceTypeName(deviceType)] = deviceID
	}
	if err := request.Config.UpdateSnapshot(config.Snapshot{
		TxbzName: request.PackageName, TxbzHash: hex.EncodeToString(request.FileHash),
		NodeID: request.NodeID, DeviceIDs: deviceIDs,
	}); err != nil {
		return globalParseFailure(request, cpdv1.ParseStage_PARSE_STAGE_SNAPSHOT, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR)
	}
	syncFilesystems()
	// Snapshot persistence is the commit point. A deadline racing immediately
	// after the atomic write must not turn an already committed parse into a
	// reported failure.
	return ParseOutcome{Result: cpdv1.Result_RESULT_SUCCESS, TypeResults: typeResults}
}

func globalParseFailure(request ParseRequest, stage cpdv1.ParseStage, code cpdv1.ErrorCode) ParseOutcome {
	results := make([]*cpdv1.ParseTypeResult, len(request.Config.DeviceTypes))
	for index, deviceType := range request.Config.DeviceTypes {
		results[index] = &cpdv1.ParseTypeResult{
			DeviceType: deviceType, DeviceId: request.Bindings[deviceType], Result: cpdv1.Result_RESULT_FAILED,
			Stage: stage, ErrorCode: code,
		}
	}
	return ParseOutcome{Result: cpdv1.Result_RESULT_FAILED, TypeResults: results, ErrorCode: code}
}
