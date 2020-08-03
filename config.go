package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/decred/dcrd/dcrutil/v3"
	"github.com/jessevdk/go-flags"
)

const (
	defaultConfigFilename = "dcrlnhub.conf"
	defaultLogLevel       = "info"
	defaultLogFilename    = "dcrlnhub.log"
	defaultBindAddr       = ":80"
	defaultUseLeHTTPS     = false

	defaultDcrlndRPCHost = "127.0.0.1:10009"
)

var (
	defaultDataDir    = dcrutil.AppDataDir("dcrlnhub", false)
	defaultConfigFile = filepath.Join(
		defaultDataDir, defaultConfigFilename,
	)
	defaultLogPath = filepath.Join(
		defaultDataDir, "logs", "decred", "testnet",
		defaultLogFilename,
	)
	defaultDcrlndDir         = dcrutil.AppDataDir("dcrlnd", false)
	defaultDcrlndTLSCertPath = filepath.Join(
		defaultDcrlndDir, "tls.cert",
	)
	defaultDcrlndMacaroonPath = filepath.Join(
		defaultDcrlndDir, "data",
		"chain", "decred",
		"testnet", "admin.macaroon",
	)
)

type config struct {
	ConfigFile   string `short:"C" long:"configfile" description:"path to config file (default:.dcrlnhub/dcrlnhub.conf)"`
	BindAddr     string `long:"bind_addr" description:"port to listen for http"`
	RPCHost      string `long:"rpchost" description:"dcrlnd's rpc listening address."`
	TLSCertPath  string `long:"certpath" description:"TLS certificate path for dcrlnd's RPC and REST services"`
	MacaroonPath string `long:"macpath" decription:"path to macaroon file to authenticate services"`
	UseLeHTTPS   bool   `long:"use_le_https" description:"use https via lets encrypt"`
	Domain       string `long:"domain" description:"the domain of the hub, required for TLS"`

	Network string
	MainNet bool `long:"mainnet" description:"use the main network."`
	TestNet bool `long:"testnet" description:"use the test network."`
	SimNet  bool `long:"simnet" description:"use the simulation network."`
}

func loadConfig() (*config, []string, error) {
	// Default config.
	cfg := config{
		BindAddr:     defaultBindAddr,
		TLSCertPath:  defaultDcrlndTLSCertPath,
		MacaroonPath: defaultDcrlndMacaroonPath,
		UseLeHTTPS:   defaultUseLeHTTPS,
	}

	// Pre-parse the command line options to see if an alternative config
	// file was specified.  Any errors aside from the
	// help message error can be ignored here since they will be caught by
	// the final parse below.
	preCfg := cfg
	preParser := flags.NewParser(&preCfg, flags.HelpFlag)
	_, err := preParser.Parse()
	if err != nil {
		if e, ok := err.(*flags.Error); ok && e.Type == flags.ErrHelp {
			fmt.Fprintln(os.Stderr, err)
			return nil, nil, err
		}
	}

	appName := filepath.Base(os.Args[0])
	appName = strings.TrimSuffix(appName, filepath.Ext(appName))
	usageMessage := fmt.Sprintf("Use %s -h to show usage", appName)

	// If the config file path has not been modified by user, then
	// we'll use the default config file path.
	if preCfg.ConfigFile == "" {
		preCfg.ConfigFile = defaultConfigFile
	}

	// Load additional config from file.
	var configFileError error
	parser := flags.NewParser(&cfg, flags.Default)

	err = flags.NewIniParser(parser).ParseFile(preCfg.ConfigFile)
	if err != nil {
		if _, ok := err.(*os.PathError); !ok {
			fmt.Fprintf(os.Stderr, "Error parsing config "+
				"file: %v\n", err)
			fmt.Fprintln(os.Stderr, usageMessage)
			return nil, nil, err
		}
		configFileError = err
	}

	// Parse command line options again to ensure they take precedence.
	remainingArgs, err := parser.Parse()
	if err != nil {
		if e, ok := err.(*flags.Error); !ok || e.Type != flags.ErrHelp {
			fmt.Fprintln(os.Stderr, err, usageMessage)
		}
		return nil, nil, err
	}

	funcName := "loadConfig"

	// Multiple networks can't be selected simultaneously.
	// Count number of network flags and set active network.
	numNets := 0
	if cfg.MainNet {
		numNets++
		cfg.Network = "mainnet"
	}
	if cfg.TestNet {
		numNets++
		cfg.Network = "testnet"
	}
	if cfg.SimNet {
		numNets++
		cfg.Network = "simnet"
	}
	if numNets > 1 {
		str := "%s, mainnet, testnet and simnet params can't be " +
			"used together -- choose one of the three"
		err := fmt.Errorf(str, funcName)
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// Update dcrlnd config paths from TLSCert and macaroons
	cfg.TLSCertPath = strings.Replace(
		cfg.TLSCertPath, "testnet",
		cfg.Network, 1,
	)
	cfg.MacaroonPath = strings.Replace(
		cfg.MacaroonPath, "testnet",
		cfg.Network, 1,
	)

	// Create the home directory if it doesn't already exist.
	err = os.MkdirAll(defaultDataDir, 0700)
	if err != nil {
		// Show a nicer error message if it's because a symlink is
		// linked to a directory that does not exist (probably because
		// it's not mounted).
		if e, ok := err.(*os.PathError); ok && os.IsExist(err) {
			if link, lerr := os.Readlink(e.Path); lerr == nil {
				str := "is symlink %s -> %s mounted?"
				err = fmt.Errorf(str, e.Path, link)
			}
		}

		str := "%s: Failed to create home directory: %v"
		err := fmt.Errorf(str, funcName, err)
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, err
	}

	// Initialize log rotation.  After log rotation has been initialized, the
	// logger variables may be used.
	defaultLogPath = strings.Replace(defaultLogPath, "testnet", cfg.Network, 1)
	initLogRotator(defaultLogPath)
	setLogLevels(defaultLogLevel)

	if cfg.UseLeHTTPS && cfg.Domain == "" {
		err := fmt.Errorf("%s: domain must be specified to use Let's Encrypt HTTPS", funcName)
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, err
	}

	// Warn about missing config file only after all other configuration is
	// done.  This prevents the warning on help messages and invalid
	// options.  Note this should go directly before the return.
	if configFileError != nil {
		log.Warnf("%v", configFileError)
	}

	return &cfg, remainingArgs, nil
}
