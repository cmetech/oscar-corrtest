package command

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/service"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

// ServeFunc starts the embedded web application.
type ServeFunc func(context.Context, web.Options) error

// ApplicationRuntime is the secret-safe durable service surface used by CLI commands.
type ApplicationRuntime interface {
	CreateTarget(context.Context, domain.TargetInput) (domain.Target, error)
	ListTargets(context.Context) ([]domain.Target, error)
	ListRuns(context.Context, domain.RunFilter) ([]domain.Run, error)
	GetRun(context.Context, string) (domain.Run, error)
	ListRunEvents(context.Context, string) ([]domain.RunEvent, error)
	ListArtifactEvidence(context.Context, string) ([]domain.ArtifactEvidence, error)
	ReadyStatus() (bool, string)
	Backup(context.Context, string) error
	Close() error
}

// OpenRuntimeFunc initializes durable services from effective configuration.
type OpenRuntimeFunc func(context.Context, config.Settings) (ApplicationRuntime, error)

// App is the oscar-corrtest command-line application.
type App struct {
	stdout  io.Writer
	stderr  io.Writer
	info    version.Info
	serve   ServeFunc
	open    OpenRuntimeFunc
	getenv  func(string) string
	service func() (service.Manager, error)
	logger  *slog.Logger
}

// Dependencies contains process-owned integrations used by commands.
type Dependencies struct {
	Serve   ServeFunc
	Open    OpenRuntimeFunc
	Getenv  func(string) string
	Service func() (service.Manager, error)
	Logger  *slog.Logger
}

// NewApplication constructs the complete CLI from explicit dependencies.
func NewApplication(stdout, stderr io.Writer, info version.Info, dependencies Dependencies) *App {
	getenv := dependencies.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return &App{stdout: stdout, stderr: stderr, info: info, serve: dependencies.Serve, open: dependencies.Open, getenv: getenv, service: dependencies.Service, logger: dependencies.Logger}
}

// New constructs an application using the supplied output streams and build metadata.
func New(stdout, stderr io.Writer, info version.Info, serve ServeFunc) *App {
	return NewApplication(stdout, stderr, info, Dependencies{Serve: serve})
}

// NewConfigured constructs the complete CLI with durable runtime initialization.
func NewConfigured(stdout, stderr io.Writer, info version.Info, serve ServeFunc, open OpenRuntimeFunc, getenv func(string) string) *App {
	return NewApplication(stdout, stderr, info, Dependencies{Serve: serve, Open: open, Getenv: getenv})
}

// Run executes a command and returns its process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if handled, exitCode := HandleHelp(a.stdout, a.stderr, args); handled {
		return exitCode
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(a.stdout, "oscar-corrtest %s commit=%s built=%s\n", a.info.Version, a.info.Commit, a.info.BuildDate)
		return 0
	case "serve":
		return a.runServe(ctx, args[1:])
	case "service":
		return a.runService(ctx, args[1:])
	case "target":
		return a.runTarget(ctx, args[1:])
	case "runs":
		return a.runRuns(ctx, args[1:])
	case "doctor":
		return a.runDoctor(ctx, args[1:])
	case "cleanup":
		return a.runCleanup(ctx, args[1:])
	case "retention":
		return a.runRetention(ctx, args[1:])
	case "backup":
		return a.runBackup(ctx, args[1:])
	case "export":
		return a.runExport(ctx, args[1:])
	case "verify-bundle":
		return a.runVerifyBundle(ctx, args[1:])
	case "scenario":
		return a.runScenario(ctx, args[1:])
	case "plan":
		return a.runPlan(ctx, args[1:])
	case "run":
		return a.runCorrelation(ctx, args[1:])
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\nRun 'oscar-corrtest --help' to list commands.\n", args[0])
		return 2
	}
}

func (a *App) runServe(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	listen := flags.String("listen", "", "HTTP listen address")
	configPath := flags.String("config", "", "configuration file")
	dataDir := flags.String("data-dir", "", "local state directory")
	remoteMode := flags.String("remote-mode", "", "optional bearer or trusted-proxy UI authentication")
	authTokenEnv := flags.String("auth-token-env", "", "environment variable containing the bearer credential")
	authTokenFile := flags.String("auth-token-file", "", "absolute file containing the bearer credential")
	authTokenSystemd := flags.String("auth-token-systemd", "", "systemd credential name containing the bearer credential")
	proxyHeader := flags.String("proxy-header", "", "canonical trusted proxy identity header")
	proxyValue := flags.String("proxy-value", "", "exact trusted proxy identity value")
	trustedProxy := flags.String("trusted-proxy", "", "comma-separated trusted proxy CIDRs")
	tlsCert := flags.String("tls-cert", "", "TLS certificate file")
	tlsKey := flags.String("tls-key", "", "TLS private key file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "serve does not accept positional arguments")
		return 2
	}
	settings, err := config.Load(a.getenv, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir, ListenAddress: *listen})
	if err != nil {
		fmt.Fprintf(a.stderr, "serve: %v\n", err)
		return 2
	}
	security, err := a.resolveServingSecurity(settings.ListenAddress, *remoteMode, secretFlags{env: *authTokenEnv, file: *authTokenFile, systemd: *authTokenSystemd}, *proxyHeader, *proxyValue, *trustedProxy, *tlsCert, *tlsKey)
	if err != nil {
		fmt.Fprintf(a.stderr, "serve: %v\n", err)
		return 2
	}
	if a.serve == nil {
		fmt.Fprintln(a.stderr, "serve is unavailable")
		return 1
	}
	if security.Mode == web.SecurityNone && !isLiteralLoopback(settings.ListenAddress) {
		fmt.Fprintln(a.stderr, "WARNING: corrtest UI is unauthenticated; every network peer that can reach this listener can create rules and inject alerts")
	}
	var application ApplicationRuntime
	if a.open != nil {
		application, err = a.open(ctx, settings)
		if err != nil {
			fmt.Fprintf(a.stderr, "serve: %v\n", err)
			return 1
		}
		defer application.Close()
	}
	scheme := "http"
	if *tlsCert != "" {
		scheme = "https"
	}
	fmt.Fprintf(a.stdout, "listening on %s://%s\n", scheme, settings.ListenAddress)
	if err := a.serve(ctx, web.Options{ListenAddress: settings.ListenAddress, Version: a.info, Data: application, Security: security, TLSCertFile: *tlsCert, TLSKeyFile: *tlsKey, Logger: a.logger}); err != nil {
		fmt.Fprintf(a.stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

type secretFlags struct{ env, file, systemd string }

func (a *App) resolveServingSecurity(_ string, mode string, refs secretFlags, proxyHeader, proxyValue, trustedProxy, tlsCert, tlsKey string) (web.Security, error) {
	if mode == "" {
		if refs.env != "" || refs.file != "" || refs.systemd != "" || proxyHeader != "" || proxyValue != "" || trustedProxy != "" || tlsCert != "" || tlsKey != "" {
			return web.Security{}, fmt.Errorf("remote authentication or TLS flags require --remote-mode")
		}
		return web.Security{}, nil
	}
	if (tlsCert == "") != (tlsKey == "") {
		return web.Security{}, fmt.Errorf("--tls-cert and --tls-key must be provided together")
	}
	switch mode {
	case string(web.SecurityBearer):
		if tlsCert == "" {
			return web.Security{}, fmt.Errorf("bearer remote mode requires --tls-cert and --tls-key")
		}
		token, err := a.resolveSecret(refs)
		if err != nil {
			return web.Security{}, err
		}
		return web.Security{Mode: web.SecurityBearer, BearerToken: token, SecureCookies: true}, nil
	case string(web.SecurityTrustedProxy):
		if refs.env != "" || refs.file != "" || refs.systemd != "" {
			return web.Security{}, fmt.Errorf("trusted-proxy mode does not accept bearer credential flags")
		}
		var networks []*net.IPNet
		for _, value := range strings.Split(trustedProxy, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			_, network, err := net.ParseCIDR(value)
			if err != nil {
				return web.Security{}, fmt.Errorf("invalid trusted proxy CIDR %q", value)
			}
			networks = append(networks, network)
		}
		security := web.Security{Mode: web.SecurityTrustedProxy, ProxyHeader: proxyHeader, ProxyValue: proxyValue, TrustedProxies: networks, SecureCookies: tlsCert != ""}
		if proxyHeader == "" || httpCanonicalHeader(proxyHeader) != proxyHeader || proxyValue == "" || len(networks) == 0 {
			return web.Security{}, fmt.Errorf("trusted-proxy mode requires --proxy-header, --proxy-value, and --trusted-proxy CIDRs")
		}
		return security, nil
	default:
		return web.Security{}, fmt.Errorf("remote mode must be bearer or trusted-proxy")
	}
}

func (a *App) resolveSecret(refs secretFlags) ([]byte, error) {
	count := 0
	for _, value := range []string{refs.env, refs.file, refs.systemd} {
		if value != "" {
			count++
		}
	}
	if count != 1 {
		return nil, fmt.Errorf("bearer mode requires exactly one credential reference")
	}
	var value []byte
	if refs.env != "" {
		value = []byte(a.getenv(refs.env))
	} else {
		path := refs.file
		if refs.systemd != "" {
			directory := a.getenv("CREDENTIALS_DIRECTORY")
			if directory == "" || filepath.Base(refs.systemd) != refs.systemd {
				return nil, fmt.Errorf("systemd credential reference is unavailable or invalid")
			}
			path = filepath.Join(directory, refs.systemd)
		}
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("credential file path must be absolute")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 64<<10 {
			return nil, fmt.Errorf("credential file is unavailable, unsafe, or too large")
		}
		// #nosec G304 -- the explicit absolute credential reference was lstat-validated above.
		value, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read credential file")
		}
	}
	value = bytes.TrimSpace(value)
	if len(value) < 16 {
		return nil, fmt.Errorf("resolved bearer credential must contain at least 16 bytes")
	}
	return value, nil
}

func httpCanonicalHeader(value string) string {
	parts := strings.Split(strings.ToLower(value), "-")
	for index := range parts {
		if parts[index] != "" {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, "-")
}

func isLiteralLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
