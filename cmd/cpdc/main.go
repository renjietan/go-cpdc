package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"cpdc/internal/buildinfo"
	"cpdc/internal/client"
	"cpdc/internal/config"
	"cpdc/internal/fsutil"
	"cpdc/internal/netio"
	"cpdc/internal/selfupdate"
)

func main() {
	if err := run(); err != nil {
		slog.Error("CPDC stopped", "error", err)
		os.Exit(1)
	}
}

func run() (result error) {
	interfaceName := flag.String("interface", "", "optional interface name; defaults to all eligible broadcast IPv4 interfaces")
	logLevel := flag.String("log-level", "info", "debug, info, warn, or error")
	configPath := flag.String("config-path", "", "optional path to cpdc_config.json (a directory or a file); defaults to the executable directory")
	selfUpdateFinalize := flag.Bool("self-update-finalize", false, "run the self-update finalize helper and exit")
	finalizeParams := flag.String("params", "", "path to the finalize parameter file")
	flag.Parse()

	level, err := parseLogLevel(*logLevel)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	if *selfUpdateFinalize {
		if err := selfupdate.RunFinalize(*finalizeParams); err != nil {
			logger.Error("self-update finalize failed", "error", err)
			return err
		}
		return nil
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	root := filepath.Dir(executable)

	cfg, err := config.LoadPath(*configPath, root, rand.Reader)
	if err != nil {
		return err
	}
	dataRoot := root
	if cfg.CachePath != "" {
		dataRoot = cfg.CachePath
		if err := os.MkdirAll(dataRoot, 0o755); err != nil {
			return fmt.Errorf("create cache path %s: %w", dataRoot, err)
		}
	}
	for _, dir := range []string{dataRoot, filepath.Join(dataRoot, "txbz"), filepath.Join(dataRoot, "txbz_ccu"), filepath.Join(dataRoot, "txbz_server")} {
		if err := fsutil.CleanupKnownTemps(dir); err != nil {
			return fmt.Errorf("clean stale temporary files in %s: %w", dir, err)
		}
	}
	candidates, err := netio.SelectCandidates(*interfaceName)
	if err != nil {
		return err
	}
	transport, err := netio.Listen(candidates, *interfaceName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, transport.Close())
	}()

	nonce := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate instance nonce: %w", err)
	}
	displayCandidate := candidates[0]
	cpdc, err := client.New(client.Options{
		Root: dataRoot, Config: cfg, TarPath: cfg.TarPath, ExecutableRoot: root, IP: displayCandidate.IPString(), SubnetMask: displayCandidate.SubnetMaskString(),
		InstanceNonce: nonce, WorkspaceFactor: buildinfo.WorkspaceFactor(),
	})
	if err != nil {
		return err
	}
	interfaces := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		interfaces = append(interfaces, candidate.Name+" ("+candidate.IPString()+")")
	}
	logger.Info("CPDC started", "target", buildinfo.Target, "interfaces", interfaces, "display_ip", displayCandidate.IPString(), "device_types", cfg.DeviceTypes)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startSelfUpdate(ctx, root, *configPath, logger)
	return client.Run(ctx, transport, cpdc, logger)
}

// startSelfUpdate wires up the self-update listener, startup receipt, and
// staging cleanup. Failures disable self-update but never stop the main
// cpd.proto distribution loop.
func startSelfUpdate(ctx context.Context, root, configPath string, logger *slog.Logger) {
	if err := selfupdate.CleanupInstallDir(root); err != nil {
		logger.Warn("clean self-update staging failed", "error", err)
	}

	runCommand, _ := selfupdate.ParseRunCommand(os.Args)
	deviceType, err := selfupdate.DetectDeviceType(configPath)
	if err != nil {
		logger.Warn("self-update disabled", "reason", "device_type", "error", err)
		return
	}
	version, err := selfupdate.ReadVersion(configPath)
	if err != nil {
		logger.Warn("self-update disabled", "reason", "version", "error", err)
		return
	}
	ip, err := selfupdate.DetectLocalIP()
	if err != nil {
		logger.Warn("self-update disabled", "reason", "local_ip", "error", err)
		return
	}
	info := selfupdate.DeviceInfo{
		DeviceType: deviceType,
		IP:         ip,
		Version:    version,
		RunCommand: runCommand,
		ConfigPath: configPath,
	}
	session, err := selfupdate.NewSession(info)
	if err != nil {
		logger.Warn("self-update disabled", "reason", "udp_bind", "error", err)
		return
	}
	for _, addr := range selfupdate.ReceiptDestinations() {
		session.SendStartupReceipt(addr, root, configPath)
	}
	go func() {
		if err := session.Run(ctx); err != nil {
			logger.Error("self-update session stopped", "error", err)
		}
	}()
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q", value)
	}
}
