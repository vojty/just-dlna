// Command just-dlna is a small DLNA media server that streams video files from a
// folder as-is, with external and embedded subtitle support.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anacrolix/dms/dlna/dms"
	"github.com/lmittmann/tint"

	"just-dlna/internal/admin"
	"just-dlna/internal/cds"
	"just-dlna/internal/library"
	"just-dlna/internal/media"
	"just-dlna/internal/netif"
)

type config struct {
	configFile    string
	mediaPath     string
	friendlyName  string
	httpPort      int
	mediaPort     int
	cacheDir      string
	subCharset    string
	prefetchSubs  bool
	logLevel      string
	dmsLogLevel   string
	logFormat     string
	logHeaders    bool
	ifname        string
	uiPort        int
	uiDir         string
	configMissing bool         // configFile was given but does not exist yet
	store         *configStore // settings for the admin UI
}

// env returns the environment variable key, or def when unset.
func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// envKeys maps flag names to the environment variables that override their
// defaults. Only these flags can be set from the config file.
var envKeys = map[string]string{}

// flagOrder lists the names in envKeys in definition order.
var flagOrder []string

func registerKey(name, key string) {
	envKeys[name] = key
	flagOrder = append(flagOrder, name)
}

func stringFlag(p *string, name, key, def, usage string) {
	flag.StringVar(p, name, env(key, def), usage+" ["+key+"]")
	registerKey(name, key)
}

func intFlag(p *int, name, key string, def int, usage string) {
	flag.IntVar(p, name, envInt(key, def), usage+" ["+key+"]")
	registerKey(name, key)
}

func boolFlag(p *bool, name, key string, def bool, usage string) {
	flag.BoolVar(p, name, envBool(key, def), usage+" ["+key+"]")
	registerKey(name, key)
}

// parseConfig reads settings with this precedence: command line flags,
// environment variables, the YAML config file, built-in defaults.
func parseConfig() (config, error) {
	var c config
	defaultCache := filepath.Join(os.TempDir(), "just-dlna-cache")
	if d, err := os.UserCacheDir(); err == nil {
		defaultCache = filepath.Join(d, "just-dlna")
	}
	flag.StringVar(&c.configFile, "config", env("CONFIG_FILE", ""), "YAML config file with flag names as keys (default: first of "+strings.Join(configSearchPaths(), ", ")+", if present) [CONFIG_FILE]")
	stringFlag(&c.mediaPath, "path", "MEDIA_PATH", ".", "media folder to serve")
	stringFlag(&c.friendlyName, "name", "FRIENDLY_NAME", "", "server name shown on clients (default: hostname based)")
	intFlag(&c.httpPort, "http-port", "HTTP_PORT", 1338, "DLNA control/description HTTP port")
	intFlag(&c.mediaPort, "media-port", "MEDIA_PORT", 1339, "video/subtitle streaming HTTP port")
	stringFlag(&c.cacheDir, "cache", "CACHE_DIR", defaultCache, "cache folder for converted subtitles")
	stringFlag(&c.subCharset, "sub-charset", "SUB_CHARSET", "", "charset of non-UTF-8 subtitle files, e.g. cp1250; converted to UTF-8")
	boolFlag(&c.prefetchSubs, "prefetch-subs", "PREFETCH_SUBS", true, "extract embedded subtitles when a client opens a video's details")
	stringFlag(&c.logLevel, "log-level", "LOG_LEVEL", "info", "debug, info, warn or error")
	stringFlag(&c.dmsLogLevel, "dms-log-level", "DMS_LOG_LEVEL", "info", "minimum level for the DLNA/SSDP library logs, on top of -log-level")
	stringFlag(&c.logFormat, "log-format", "LOG_FORMAT", "auto", "auto, pretty, text or json; auto is pretty on a terminal, text otherwise")
	boolFlag(&c.logHeaders, "log-headers", "LOG_HEADERS", false, "dump DLNA HTTP headers to stderr (client debugging)")
	stringFlag(&c.ifname, "ifname", "IFNAME", "", "comma-separated network interfaces to announce on, e.g. eth0,wlo1 (default: LAN interfaces, skipping Thread, Docker and VPN ones)")
	intFlag(&c.uiPort, "ui-port", "UI_PORT", 1340, "web UI port, 0 disables the web UI")
	stringFlag(&c.uiDir, "ui-dir", "UI_DIR", "ui/dist", "folder with the built web UI")
	flag.Parse()

	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	if c.configFile == "" {
		c.configFile = findConfigFile()
	}
	if c.configFile != "" {
		// A given but missing file is created when settings are saved.
		if _, err := os.Stat(c.configFile); errors.Is(err, fs.ErrNotExist) {
			c.configMissing = true
		} else if err := applyConfigFile(flag.CommandLine, c.configFile, envKeys); err != nil {
			return c, err
		}
	}
	file := c.configFile
	if file == "" {
		file = defaultConfigFile()
	}
	c.store = &configStore{
		file:     file,
		fset:     flag.CommandLine,
		envKeys:  envKeys,
		order:    flagOrder,
		explicit: explicit,
	}
	return c, nil
}

func parseLevel(s string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("invalid log level %q", s)
	}
	return level, nil
}

// isTerminal reports whether f is attached to a terminal.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func newLogger(c config) (*slog.Logger, error) {
	level, err := parseLevel(c.logLevel)
	if err != nil {
		return nil, err
	}
	format := strings.ToLower(c.logFormat)
	if format == "auto" {
		format = "text"
		if isTerminal(os.Stderr) {
			format = "pretty"
		}
	}
	switch format {
	case "pretty":
		// Human-readable output: short local time, colored levels unless
		// stderr is not a terminal or NO_COLOR is set (https://no-color.org).
		_, noColor := os.LookupEnv("NO_COLOR")
		return slog.New(tint.NewHandler(os.Stderr, &tint.Options{
			Level:      level,
			TimeFormat: "15:04:05.000",
			NoColor:    noColor || !isTerminal(os.Stderr),
		})), nil
	case "text":
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})), nil
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})), nil
	default:
		return nil, fmt.Errorf("invalid log format %q", c.logFormat)
	}
}

// minLevelHandler drops records below min, on top of the wrapped handler's
// own level. It quiets chatty third-party components independently of the
// global level.
type minLevelHandler struct {
	slog.Handler
	min slog.Level
}

func (h minLevelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return minLevelHandler{h.Handler.WithAttrs(attrs), h.min}
}

func (h minLevelHandler) WithGroup(name string) slog.Handler {
	return minLevelHandler{h.Handler.WithGroup(name), h.min}
}

func main() {
	c, err := parseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logger, err := newLogger(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	slog.SetDefault(logger)
	err = run(c, logger)
	if errors.Is(err, errRestart) {
		err = restart(logger)
	}
	if err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

// errRestart is returned by run when the web UI asked for a restart.
var errRestart = errors.New("restart requested")

// restart replaces the process with a fresh copy of itself, which reads the
// saved config file again. Arguments and environment are kept.
func restart(logger *slog.Logger) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	logger.Info("restarting")
	return fmt.Errorf("restart: %w", syscall.Exec(exe, os.Args, os.Environ()))
}

func run(c config, logger *slog.Logger) error {
	dmsLevel, err := parseLevel(c.dmsLogLevel)
	if err != nil {
		return err
	}
	lib, err := library.Open(c.mediaPath, logger.With("component", "library"))
	if err != nil {
		return err
	}
	defer lib.Close()

	extractor, err := media.NewExtractor(lib, c.cacheDir, c.subCharset, logger.With("component", "subtitles"))
	if err != nil {
		return err
	}
	mediaSrv := &media.Server{
		Lib:       lib,
		Extractor: extractor,
		Port:      c.mediaPort,
		Logger:    logger.With("component", "media"),
	}
	browser := &cds.Browser{
		Lib:          lib,
		Media:        mediaSrv,
		PrefetchSubs: c.prefetchSubs,
		Logger:       logger.With("component", "cds"),
	}

	mediaLn, err := net.Listen("tcp", ":"+strconv.Itoa(c.mediaPort))
	if err != nil {
		return fmt.Errorf("media listener: %w", err)
	}
	dlnaLn, err := net.Listen("tcp", ":"+strconv.Itoa(c.httpPort))
	if err != nil {
		return fmt.Errorf("dlna listener: %w", err)
	}
	// dms listens on loopback only, behind a proxy that passes just the
	// UPnP endpoints (see dmsproxy.go).
	dmsInner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("dlna listener: %w", err)
	}
	dlnaProxy := &http.Server{
		Handler:           newDMSProxy(dmsInner.Addr().String()),
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.With("component", "dlna-http").Handler(), slog.LevelWarn),
	}

	ifaces, err := selectInterfaces(c.ifname, logger)
	if err != nil {
		return err
	}
	mediaSrv.Interfaces = netif.Names(ifaces)
	dmsIfaces := make([]net.Interface, len(ifaces))
	for i, ifi := range ifaces {
		dmsIfaces[i] = ifi.Interface
	}

	dmsSrv := &dms.Server{
		HTTPConn:               publicAddrListener{dmsInner, dlnaLn.Addr()},
		FriendlyName:           c.friendlyName,
		Interfaces:             dmsIfaces, // non-nil: dms uses all interfaces for nil
		RootObjectPath:         c.mediaPath,
		FS:                     lib.FS(),
		OnBrowseDirectChildren: browser.BrowseDirectChildren,
		OnBrowseMetadata:       browser.BrowseMetadata,
		NoTranscode:            true,
		NoProbe:                true,
		IgnoreHidden:           true,
		AllowedIpNets:          allNets,
		LogHeaders:             c.logHeaders,
		NotifyInterval:         30 * time.Second,
		Logger:                 slog.New(minLevelHandler{logger.Handler(), dmsLevel}).With("component", "dms"),
	}
	if err := dmsSrv.Init(); err != nil {
		return fmt.Errorf("dms init: %w", err)
	}

	restartc := make(chan struct{}, 1)
	var adminSrv *http.Server
	var adminLn net.Listener
	if c.uiPort != 0 {
		adm, err := admin.New(c.mediaPath, c.uiDir, c.store, func() {
			select {
			case restartc <- struct{}{}:
			default:
			}
		}, logger.With("component", "admin"))
		if err != nil {
			return err
		}
		defer adm.Close()
		adminLn, err = net.Listen("tcp", ":"+strconv.Itoa(c.uiPort))
		if err != nil {
			return fmt.Errorf("web UI listener: %w", err)
		}
		// No write timeout: uploads and downloads of large videos take long.
		adminSrv = &http.Server{
			Handler:           adm.Handler(),
			ReadHeaderTimeout: 30 * time.Second,
			ErrorLog:          slog.NewLogLogger(logger.With("component", "admin-http").Handler(), slog.LevelWarn),
		}
	}

	httpSrv := &http.Server{
		Handler:           mediaSrv.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.With("component", "media-http").Handler(), slog.LevelWarn),
	}

	abs, _ := filepath.Abs(c.mediaPath)
	logger.Info("starting",
		"config", c.configFile,
		"media_path", abs,
		"name", dmsSrv.FriendlyName,
		"dlna_port", c.httpPort,
		"media_port", c.mediaPort,
		"cache", c.cacheDir,
		"prefetch_subs", c.prefetchSubs,
		"interfaces", netif.Names(ifaces),
		"ui_port", c.uiPort,
	)
	if c.configMissing {
		logger.Warn("config file not found, it is created when settings are saved in the web UI", "config", c.configFile)
	}

	errc := make(chan error, 4)
	go func() {
		if err := dlnaProxy.Serve(dlnaLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("dlna proxy: %w", err)
		}
	}()
	go func() {
		if err := httpSrv.Serve(mediaLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("media server: %w", err)
		}
	}()
	if adminSrv != nil {
		go func() {
			if err := adminSrv.Serve(adminLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("web UI server: %w", err)
			}
		}()
	}
	go func() {
		if err := dmsSrv.Run(); err != nil {
			errc <- fmt.Errorf("dlna server: %w", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err = <-errc:
	case <-restartc:
		err = errRestart
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if serr := httpSrv.Shutdown(shutdownCtx); serr != nil {
		logger.Warn("media server shutdown", "error", serr)
	}
	if serr := dlnaProxy.Shutdown(shutdownCtx); serr != nil {
		logger.Warn("dlna proxy shutdown", "error", serr)
	}
	if adminSrv != nil {
		if serr := adminSrv.Shutdown(shutdownCtx); serr != nil {
			logger.Warn("web UI server shutdown", "error", serr)
		}
	}
	if cerr := dmsSrv.Close(); cerr != nil {
		logger.Debug("dlna server close", "error", cerr)
	}
	return err
}

// selectInterfaces picks the interfaces for SSDP announcements, see
// netif.Select, and logs the choice.
func selectInterfaces(ifname string, logger *slog.Logger) ([]netif.Interface, error) {
	all, err := netif.List()
	if err != nil {
		return nil, fmt.Errorf("network interfaces: %w", err)
	}
	names := netif.ParseNames(ifname)
	chosen, skipped, err := netif.Select(all, names)
	if err != nil {
		return nil, err
	}
	for _, s := range skipped {
		logger.Info("interface skipped", "interface", s.Name, "reason", s.Reason)
	}
	for _, ifi := range chosen {
		if r := netif.Check(ifi); r != "" {
			logger.Warn("announcing on an interface that does not look like a LAN", "interface", ifi.Name, "reason", r)
		} else {
			logger.Info("interface chosen", "interface", ifi.Name)
		}
	}
	if len(chosen) == 0 {
		logger.Warn("no LAN interface found, clients cannot discover the server; set -ifname / IFNAME")
	}
	return chosen, nil
}
