package output

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/packagezip"
)

type Request struct {
	Root           string
	Package        *packagezip.Package
	PackageName    string
	NodeID         string
	DeviceTypes    []cpdv1.DeviceType
	Bindings       map[cpdv1.DeviceType]string
	ReReadjsonPath string
	TarPath        string
	DefaultTarRoot string
}

type typeError struct {
	stage cpdv1.ParseStage
	code  cpdv1.ErrorCode
	err   error
}

func (e *typeError) Error() string { return e.err.Error() }
func (e *typeError) Unwrap() error { return e.err }

func Process(ctx context.Context, request Request) ([]*cpdv1.ParseTypeResult, cpdv1.ErrorCode) {
	results := make([]*cpdv1.ParseTypeResult, 0, len(request.DeviceTypes))
	failed := false
	topLevel := cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED
	for _, deviceType := range request.DeviceTypes {
		deviceID := request.Bindings[deviceType]
		if failed {
			results = append(results, &cpdv1.ParseTypeResult{
				DeviceType: deviceType, DeviceId: deviceID, Result: cpdv1.Result_RESULT_FAILED,
				Stage: cpdv1.ParseStage_PARSE_STAGE_SKIPPED, ErrorCode: cpdv1.ErrorCode_ERROR_CODE_SKIPPED_AFTER_PREVIOUS_FAILURE,
			})
			continue
		}
		if err := ctx.Err(); err != nil {
			failed = true
			topLevel = cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT
			results = append(results, failedResult(deviceType, deviceID, cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, topLevel))
			continue
		}
		if err := validateRequest(request, deviceType, deviceID); err != nil {
			failed = true
			topLevel = cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE
			results = append(results, failedResult(deviceType, deviceID, cpdv1.ParseStage_PARSE_STAGE_VALIDATE, topLevel))
			continue
		}
		defaultTarDir := request.DefaultTarRoot
		if defaultTarDir == "" {
			defaultTarDir = request.Root
		}
		var err error
		switch deviceType {
		case cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO, cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD,
			cpdv1.DeviceType_DEVICE_TYPE_HF, cpdv1.DeviceType_DEVICE_TYPE_SMALL_HANDHELD:
			err = writeRadio(ctx, request.Root, request.Package, request.Package.Devices[deviceID], iniPathFor(request.Root, request.ReReadjsonPath), request.TarPath, defaultTarDir)
		case cpdv1.DeviceType_DEVICE_TYPE_CCU, cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO:
			err = writeCCU(ctx, request.Root, request.Package, request.Package.Devices[deviceID], deviceType)
		case cpdv1.DeviceType_DEVICE_TYPE_SERVER, cpdv1.DeviceType_DEVICE_TYPE_IEC:
			err = writeServer(ctx, request.Root, request.Package, request.PackageName, request.NodeID)
		case cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER:
			err = writeVehInter(ctx, request.Root, request.Package, request.Package.Devices[deviceID], request.NodeID, iniPathFor(request.Root, request.ReReadjsonPath), request.TarPath, defaultTarDir)
		default:
			err = &typeError{stage: cpdv1.ParseStage_PARSE_STAGE_VALIDATE, code: cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err: errors.New("unsupported device type")}
		}
		if err != nil {
			failed = true
			stage := cpdv1.ParseStage_PARSE_STAGE_GENERATE
			code := cpdv1.ErrorCode_ERROR_CODE_PARSE_OUTPUT_FAILED
			var typed *typeError
			if errors.As(err, &typed) {
				stage, code = typed.stage, typed.code
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				stage, code = cpdv1.ParseStage_PARSE_STAGE_TIMEOUT, cpdv1.ErrorCode_ERROR_CODE_PARSE_TIMEOUT
			}
			topLevel = code
			results = append(results, failedResult(deviceType, deviceID, stage, code))
			continue
		}
		results = append(results, &cpdv1.ParseTypeResult{
			DeviceType: deviceType, DeviceId: deviceID, Result: cpdv1.Result_RESULT_SUCCESS,
			Stage: cpdv1.ParseStage_PARSE_STAGE_UNSPECIFIED, ErrorCode: cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED,
		})
	}
	return results, topLevel
}

func iniPathFor(root, configured string) string {
	if configured == "" {
		return filepath.Join(root, "ReReadJson.ini")
	}
	return configured
}

func validateRequest(request Request, deviceType cpdv1.DeviceType, deviceID string) error {
	if request.Root == "" || request.Package == nil || request.NodeID == "" || deviceID == "" {
		return errors.New("incomplete output request")
	}
	if len([]byte(request.NodeID)) > packagezip.MaxComponentBytes {
		return errors.New("node ID exceeds limit")
	}
	device := request.Package.Devices[deviceID]
	expectedPackageType := deviceType
	if deviceType == cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO {
		expectedPackageType = cpdv1.DeviceType_DEVICE_TYPE_CCU
	}
	if device == nil || device.Type != expectedPackageType {
		return errors.New("binding does not match package device")
	}
	if (deviceType == cpdv1.DeviceType_DEVICE_TYPE_SERVER || deviceType == cpdv1.DeviceType_DEVICE_TYPE_IEC) &&
		(request.PackageName == "" || request.PackageName != filepath.Base(request.PackageName) || !strings.EqualFold(filepath.Ext(request.PackageName), ".zip")) {
		return fmt.Errorf("invalid original package name")
	}
	return nil
}

func failedResult(deviceType cpdv1.DeviceType, deviceID string, stage cpdv1.ParseStage, code cpdv1.ErrorCode) *cpdv1.ParseTypeResult {
	return &cpdv1.ParseTypeResult{
		DeviceType: deviceType, DeviceId: deviceID, Result: cpdv1.Result_RESULT_FAILED,
		Stage: stage, ErrorCode: code,
	}
}

func generationError(err error) error {
	return &typeError{stage: cpdv1.ParseStage_PARSE_STAGE_GENERATE, code: cpdv1.ErrorCode_ERROR_CODE_PARSE_OUTPUT_FAILED, err: err}
}

func writeError(err error) error {
	return &typeError{stage: cpdv1.ParseStage_PARSE_STAGE_WRITE_OUTPUT, code: cpdv1.ErrorCode_ERROR_CODE_OUTPUT_WRITE_FAILED, err: err}
}
