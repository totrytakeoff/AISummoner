package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/agentapi"
	"github.com/aisummoner/aisummoner/internal/app"
	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/config"
	"github.com/aisummoner/aisummoner/internal/deepseek"
	"github.com/aisummoner/aisummoner/internal/device"
	"github.com/aisummoner/aisummoner/internal/devicegate"
	"github.com/aisummoner/aisummoner/internal/httpapi"
	"github.com/aisummoner/aisummoner/internal/opencode"
	"github.com/aisummoner/aisummoner/internal/opencodebridge"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/requestsource"
	"github.com/aisummoner/aisummoner/internal/sshclient"
	"github.com/aisummoner/aisummoner/internal/staticweb"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/aisummoner/aisummoner/internal/terminal"
	"github.com/aisummoner/aisummoner/internal/tunnel"
)

const (
	startupTimeout        = 30 * time.Second
	shutdownTimeout       = 15 * time.Second
	publicHeaderTimeout   = 10 * time.Second
	publicIdleTimeout     = 60 * time.Second
	bridgeHeaderTimeout   = 5 * time.Second
	bridgeIdleTimeout     = 30 * time.Second
	maximumHTTPHeaderSize = 32 * 1024
)

type builtServer struct {
	runtime       *app.Runtime
	publicAddress string
	bridgeAddress string
	provider      string
	manager       *tunnel.Manager
}

// startupCleanup owns resources only until Runtime is completely constructed.
// Transfer clears the stack atomically from the composition root's point of
// view; after that point Runtime is the sole database/listener/domain owner.
type startupCleanup struct {
	entries     []func()
	transferred bool
}

func (cleanup *startupCleanup) Add(entry func()) {
	if entry == nil {
		return
	}
	if cleanup.transferred {
		panic("startup resource added after Runtime ownership transfer")
	}
	cleanup.entries = append(cleanup.entries, entry)
}

func (cleanup *startupCleanup) Cleanup() {
	for index := len(cleanup.entries) - 1; index >= 0; index-- {
		cleanup.entries[index]()
	}
	cleanup.entries = nil
}

func (cleanup *startupCleanup) Transfer() {
	cleanup.entries = nil
	cleanup.transferred = true
}

func run(ctx context.Context, logger *slog.Logger) error {
	if ctx == nil {
		return errors.New("server context is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	startupContext, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	server, err := buildServer(startupContext, configuration, logger)
	cancelStartup()
	if err != nil {
		return err
	}
	logServerReady(logger)

	runError := server.runtime.Run(ctx)
	// Runtime.Run intentionally leaves a pre-canceled, never-started Runtime
	// untouched. The process composition still owns it and must trigger the
	// same bounded shutdown path before returning.
	shutdownError := server.runtime.Shutdown(context.Background())
	if errors.Is(runError, context.Canceled) {
		runError = nil
	}
	if runError != nil {
		return runError
	}
	return shutdownError
}

// logServerReady deliberately records no configured address, origin, provider,
// credential, or path value. Startup configuration values belong in neither
// interactive output nor persistent service logs.
func logServerReady(logger *slog.Logger) {
	logger.Info("server ready")
}

type agentAdapterRegistry interface {
	SetAdapter(string, agent.Adapter) error
}

// deepSeekProviderConfigurator is the only Browser-to-provider credential
// handoff. The credential is captured by the Adapter in process memory and is
// never persisted, logged, audited, or returned in an HTTP response.
type deepSeekProviderConfigurator struct {
	registry agentAdapterRegistry
}

func (configurator *deepSeekProviderConfigurator) ConfigureDeepSeek(ctx context.Context, apiKey, model string) error {
	if configurator == nil || configurator.registry == nil {
		return agent.ErrServiceClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	adapter, err := deepseek.NewAdapter(deepseek.Options{
		BaseURL: deepseek.DefaultBaseURL,
		APIKey:  apiKey,
		ModelID: model,
	})
	if err != nil {
		return agent.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return configurator.registry.SetAdapter(agent.ProviderDeepSeek, adapter)
}

func buildServer(ctx context.Context, configuration config.Config, logger *slog.Logger) (_ *builtServer, err error) {
	if ctx == nil {
		return nil, errors.New("startup context is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := configuration.PreparePrivateDirectories(); err != nil {
		return nil, err
	}

	startup := &startupCleanup{}
	defer startup.Cleanup()
	database, err := store.Open(ctx, configuration.DatabasePath)
	if err != nil {
		return nil, err
	}
	startup.Add(func() { _ = database.Close() })

	authService := auth.NewService(database)
	_, created, err := authService.Bootstrap(ctx, configuration.AdminPassword, time.Now())
	if err != nil {
		return nil, err
	}
	if created {
		logger.Info("bootstrapped administrator")
	}
	pairingService, err := pairing.NewService(database, configuration.PairingSecret)
	if err != nil {
		return nil, err
	}
	sourceResolver := requestsource.New(configuration.TrustedProxyIPs)

	lifecycleGate := devicegate.New()
	connectionManager := tunnel.NewManager()
	tunnelGateway, err := tunnel.NewGateway(tunnel.GatewayOptions{
		Store: database, Pairing: pairingService, Auditor: database,
		Manager: connectionManager, DeviceGate: lifecycleGate, Logger: logger,
		SourceResolver: sourceResolver,
	})
	if err != nil {
		return nil, err
	}
	startup.Add(tunnelGateway.Close)

	sshDialer, err := sshclient.NewDialer(connectionManager, sshclient.StoreDeviceKeys{Store: database})
	if err != nil {
		return nil, err
	}
	openTerminal, err := app.NewTerminalOpener(sshDialer)
	if err != nil {
		return nil, err
	}
	terminalHandler, err := terminal.New(terminal.Options{
		Auth: authService, Devices: database, Online: connectionManager, OpenPTY: openTerminal,
		AllowedOrigin: configuration.AllowedOrigin, Logger: logger,
	})
	if err != nil {
		return nil, err
	}
	startup.Add(terminalHandler.Close)
	remoteExecutor, err := app.NewRemoteExecutor(sshDialer)
	if err != nil {
		return nil, err
	}

	var (
		agentAdapter   agent.Adapter
		provider       string
		bridge         *opencodebridge.Bridge
		bridgeServer   *http.Server
		bridgeListener net.Listener
		closeBridge    func(context.Context) error
	)
	switch configuration.AgentAdapter {
	case config.AgentAdapterFake:
		agentAdapter = &agent.FakeAdapter{}
		provider = agent.ProviderFake
	case config.AgentAdapterOpenCode:
		listenerConfig := net.ListenConfig{}
		bridgeListener, err = listenerConfig.Listen(ctx, "tcp", configuration.AgentBridgeListenAddr)
		if err != nil {
			return nil, errors.New("bind OpenCode bridge listener")
		}
		startup.Add(func() { _ = bridgeListener.Close() })
		bridge, err = opencodebridge.New(opencodebridge.Options{
			Secret: configuration.AgentBridgeSecret, Logger: logger,
		})
		if err != nil {
			return nil, err
		}
		startup.Add(func() {
			closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = bridge.Close(closeContext)
		})
		openCodeAdapter, adapterErr := opencode.NewAdapter(opencode.Options{
			BaseURL: configuration.OpenCodeURL, Username: configuration.OpenCodeUsername,
			Password: configuration.OpenCodePassword, ModelID: configuration.OpenCodeModel,
			WorkspaceRoot: configuration.AgentWorkspaceRoot, Logger: logger, Bridge: bridge,
		})
		if adapterErr != nil {
			return nil, adapterErr
		}
		if err := app.AwaitOpenCodeStartup(ctx, app.OpenCodeStartupOptions{Probe: openCodeAdapter}); err != nil {
			return nil, err
		}
		agentAdapter = openCodeAdapter
		provider = opencode.ProviderName
		closeBridge = bridge.Close
		bridgeServer = &http.Server{
			Addr: configuration.AgentBridgeListenAddr, Handler: bridge.Handler(),
			ReadHeaderTimeout: bridgeHeaderTimeout, IdleTimeout: bridgeIdleTimeout,
			MaxHeaderBytes: maximumHTTPHeaderSize,
			ErrorLog:       slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		}
	case config.AgentAdapterDeepSeek:
		deepSeekAdapter, adapterErr := deepseek.NewAdapter(deepseek.Options{
			BaseURL: configuration.DeepSeekURL, APIKey: configuration.DeepSeekAPIKey,
			ModelID: configuration.DeepSeekModel,
		})
		if adapterErr != nil {
			return nil, adapterErr
		}
		agentAdapter = deepSeekAdapter
		provider = deepseek.ProviderName
	default:
		return nil, errors.New("unsupported Agent adapter")
	}

	agentService, err := agent.NewService(agent.ServiceOptions{
		Store: database, Adapter: agentAdapter, Provider: provider,
		Executor: remoteExecutor, Online: connectionManager, Auditor: database, Logger: logger,
	})
	if err != nil {
		return nil, err
	}
	startup.Add(agentService.Close)

	deviceService, err := device.NewLifecycleService(device.LifecycleOptions{
		Store: database, Online: connectionManager, Gate: lifecycleGate,
		Tunnel: connectionManager, Terminal: terminalHandler, Agent: agentService,
		Logger: logger,
	})
	if err != nil {
		return nil, err
	}
	browserAPI, err := httpapi.New(httpapi.Options{
		Auth: authService, Pairing: pairingService, Devices: deviceService, Auditor: database,
		AllowedOrigin: configuration.AllowedOrigin, CookieSecure: configuration.CookieSecure, Logger: logger,
		SourceResolver: sourceResolver,
	})
	if err != nil {
		return nil, err
	}
	agentAPI, err := agentapi.New(agentapi.Options{
		Auth: authService, Agent: agentService,
		ProviderConfigurator: &deepSeekProviderConfigurator{registry: agentService},
		AllowedOrigin:        configuration.AllowedOrigin, Logger: logger,
	})
	if err != nil {
		return nil, err
	}
	staticHandler, err := staticweb.NewEmbedded()
	if err != nil {
		return nil, err
	}
	readiness := app.NewReadiness()
	healthHandler, err := app.NewHealth(readiness, database, time.Second)
	if err != nil {
		return nil, err
	}
	dispatcher, err := app.NewDispatcher(app.DispatcherOptions{
		Readiness: readiness, Tunnel: tunnelGateway, Terminal: terminalHandler.Handler(),
		Agent: agentAPI.Handler(), Health: healthHandler, Browser: browserAPI.Handler(), Static: staticHandler,
	})
	if err != nil {
		return nil, err
	}

	listenerConfig := net.ListenConfig{}
	publicListener, err := listenerConfig.Listen(ctx, "tcp", configuration.ListenAddr)
	if err != nil {
		return nil, errors.New("bind public listener")
	}
	startup.Add(func() { _ = publicListener.Close() })
	publicServer := &http.Server{
		Addr: configuration.ListenAddr, Handler: dispatcher,
		ReadHeaderTimeout: publicHeaderTimeout, IdleTimeout: publicIdleTimeout,
		MaxHeaderBytes: maximumHTTPHeaderSize,
		ErrorLog:       slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	runtime, err := app.NewRuntime(app.RuntimeOptions{
		Readiness: readiness, PublicServer: publicServer, PublicListener: publicListener,
		BridgeServer: bridgeServer, BridgeListener: bridgeListener,
		CloseAgent: agentService.Close, CloseTerminal: terminalHandler.Close, CloseTunnel: tunnelGateway.Close,
		CloseBridge: closeBridge, CloseDatabase: database.Close, ShutdownTimeout: shutdownTimeout,
	})
	if err != nil {
		return nil, err
	}

	// This is the only lifetime handoff. Every startup error above unwinds the
	// stack; every return below is governed solely by Runtime's bounded joined
	// shutdown, including the decision not to close SQLite under a live owner.
	startup.Transfer()
	bridgeAddress := ""
	if bridgeListener != nil {
		bridgeAddress = bridgeListener.Addr().String()
	}
	return &builtServer{
		runtime: runtime, publicAddress: publicListener.Addr().String(), bridgeAddress: bridgeAddress,
		provider: provider, manager: connectionManager,
	}, nil
}
