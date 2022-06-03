// Copyright © by Jeff Foley 2017-2022. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"goSqlite_gorm/pkg/util"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/OWASP/Amass/v3/config"
	"github.com/OWASP/Amass/v3/datasrcs"
	"github.com/OWASP/Amass/v3/enum"
	"github.com/OWASP/Amass/v3/format"
	"github.com/OWASP/Amass/v3/requests"
	"github.com/OWASP/Amass/v3/systems"
	"github.com/caffix/netmap"
	"github.com/caffix/stringset"
	"github.com/fatih/color"
)

const enumUsageMsg = "enum [options] -d DOMAIN"

type enumArgs struct {
	Addresses         format.ParseIPs
	ASNs              format.ParseInts
	CIDRs             format.ParseCIDRs
	AltWordList       *stringset.Set
	AltWordListMask   *stringset.Set
	BruteWordList     *stringset.Set
	BruteWordListMask *stringset.Set
	Blacklist         *stringset.Set
	Domains           *stringset.Set
	Excluded          *stringset.Set
	Included          *stringset.Set
	Interface         string
	MaxDNSQueries     int
	ResolverQPS       int
	TrustedQPS        int
	MaxDepth          int
	MinForRecursive   int
	Names             *stringset.Set
	Ports             format.ParseInts
	Resolvers         *stringset.Set
	Trusted           *stringset.Set
	Timeout           int
	Options           struct {
		Active          bool
		Alterations     bool
		BruteForcing    bool
		DemoMode        bool
		IPs             bool
		IPv4            bool
		IPv6            bool
		ListSources     bool
		NoAlts          bool
		NoColor         bool
		NoLocalDatabase bool
		NoRecursive     bool
		Passive         bool
		Silent          bool
		Sources         bool
		Verbose         bool
	}
	Filepaths struct {
		AllFilePrefix    string
		AltWordlist      format.ParseStrings
		Blacklist        string
		BruteWordlist    format.ParseStrings
		ConfigFile       string
		Directory        string
		Domains          format.ParseStrings
		ExcludedSrcs     string
		IncludedSrcs     string
		JSONOutput       string
		LogFile          string
		Names            format.ParseStrings
		Resolvers        format.ParseStrings
		Trusted          format.ParseStrings
		ScriptsDirectory string
		TermOut          string
	}
}

func defineEnumArgumentFlags(enumFlags *flag.FlagSet, args *enumArgs) {
	enumFlags.Var(&args.Addresses, "addr", "IPs and ranges (192.168.1.1-254) separated by commas")
	enumFlags.Var(args.AltWordListMask, "awm", "\"hashcat-style\" wordlist masks for name alterations")
	enumFlags.Var(&args.ASNs, "asn", "ASNs separated by commas (can be used multiple times)")
	enumFlags.Var(&args.CIDRs, "cidr", "CIDRs separated by commas (can be used multiple times)")
	enumFlags.Var(args.Blacklist, "bl", "Blacklist of subdomain names that will not be investigated")
	enumFlags.Var(args.BruteWordListMask, "wm", "\"hashcat-style\" wordlist masks for DNS brute forcing")
	enumFlags.Var(args.Domains, "d", "Domain names separated by commas (can be used multiple times)")
	enumFlags.Var(args.Excluded, "exclude", "Data source names separated by commas to be excluded")
	enumFlags.Var(args.Included, "include", "Data source names separated by commas to be included")
	enumFlags.StringVar(&args.Interface, "iface", "", "Provide the network interface to send traffic through")
	enumFlags.IntVar(&args.MaxDNSQueries, "max-dns-queries", 0, "Deprecated flag to be replaced by dns-qps in version 4.0")
	enumFlags.IntVar(&args.MaxDNSQueries, "dns-qps", 0, "Maximum number of DNS queries per second across all resolvers")
	enumFlags.IntVar(&args.ResolverQPS, "rqps", 0, "Maximum number of DNS queries per second for each untrusted resolver")
	enumFlags.IntVar(&args.TrustedQPS, "trqps", 0, "Maximum number of DNS queries per second for each trusted resolver")
	enumFlags.IntVar(&args.MaxDepth, "max-depth", 0, "Maximum number of subdomain labels for brute forcing")
	enumFlags.IntVar(&args.MinForRecursive, "min-for-recursive", 1, "Subdomain labels seen before recursive brute forcing (Default: 1)")
	enumFlags.Var(&args.Ports, "p", "Ports separated by commas (default: 80, 443)")
	enumFlags.Var(args.Resolvers, "r", "IP addresses of untrusted DNS resolvers (can be used multiple times)")
	enumFlags.Var(args.Resolvers, "tr", "IP addresses of trusted DNS resolvers (can be used multiple times)")
	enumFlags.IntVar(&args.Timeout, "timeout", 0, "Number of minutes to let enumeration run before quitting")
}

func defineEnumOptionFlags(enumFlags *flag.FlagSet, args *enumArgs) {
	var placeholder bool
	enumFlags.BoolVar(&args.Options.Active, "active", false, "Attempt zone transfers and certificate name grabs")
	enumFlags.BoolVar(&args.Options.BruteForcing, "brute", false, "Execute brute forcing after searches")
	enumFlags.BoolVar(&args.Options.DemoMode, "demo", false, "Censor output to make it suitable for demonstrations")
	enumFlags.BoolVar(&args.Options.IPs, "ip", false, "Show the IP addresses for discovered names")
	enumFlags.BoolVar(&args.Options.IPv4, "ipv4", false, "Show the IPv4 addresses for discovered names")
	enumFlags.BoolVar(&args.Options.IPv6, "ipv6", false, "Show the IPv6 addresses for discovered names")
	enumFlags.BoolVar(&args.Options.ListSources, "list", false, "Print the names of all available data sources")
	enumFlags.BoolVar(&args.Options.Alterations, "alts", false, "Enable generation of altered names")
	enumFlags.BoolVar(&args.Options.NoAlts, "noalts", true, "Deprecated flag to be removed in version 4.0")
	enumFlags.BoolVar(&args.Options.NoColor, "nocolor", false, "Disable colorized output")
	enumFlags.BoolVar(&placeholder, "nolocaldb", false, "Deprecated feature to be removed in version 4.0")
	enumFlags.BoolVar(&args.Options.NoRecursive, "norecursive", false, "Turn off recursive brute forcing")
	enumFlags.BoolVar(&args.Options.Passive, "passive", false, "Disable DNS resolution of names and dependent features")
	enumFlags.BoolVar(&placeholder, "share", false, "Deprecated feature to be removed in version 4.0")
	enumFlags.BoolVar(&args.Options.Silent, "silent", false, "Disable all output during execution")
	enumFlags.BoolVar(&args.Options.Sources, "src", false, "Print data sources for the discovered names")
	enumFlags.BoolVar(&args.Options.Verbose, "v", false, "Output status / debug / troubleshooting info")
}

func defineEnumFilepathFlags(enumFlags *flag.FlagSet, args *enumArgs) {
	enumFlags.StringVar(&args.Filepaths.AllFilePrefix, "oA", "", "Path prefix used for naming all output files")
	enumFlags.Var(&args.Filepaths.AltWordlist, "aw", "Path to a different wordlist file for alterations")
	enumFlags.StringVar(&args.Filepaths.Blacklist, "blf", "", "Path to a file providing blacklisted subdomains")
	enumFlags.Var(&args.Filepaths.BruteWordlist, "w", "Path to a different wordlist file for brute forcing")
	enumFlags.StringVar(&args.Filepaths.ConfigFile, "config", "", "Path to the INI configuration file. Additional details below")
	enumFlags.StringVar(&args.Filepaths.Directory, "dir", "", "Path to the directory containing the output files")
	enumFlags.Var(&args.Filepaths.Domains, "df", "Path to a file providing root domain names")
	enumFlags.StringVar(&args.Filepaths.ExcludedSrcs, "ef", "", "Path to a file providing data sources to exclude")
	enumFlags.StringVar(&args.Filepaths.IncludedSrcs, "if", "", "Path to a file providing data sources to include")
	enumFlags.StringVar(&args.Filepaths.JSONOutput, "json", "", "Path to the JSON output file")
	enumFlags.StringVar(&args.Filepaths.LogFile, "log", "", "Path to the log file where errors will be written")
	enumFlags.Var(&args.Filepaths.Names, "nf", "Path to a file providing already known subdomain names (from other tools/sources)")
	enumFlags.Var(&args.Filepaths.Resolvers, "rf", "Path to a file providing untrusted DNS resolvers")
	enumFlags.Var(&args.Filepaths.Trusted, "trf", "Path to a file providing trusted DNS resolvers")
	enumFlags.StringVar(&args.Filepaths.ScriptsDirectory, "scripts", "", "Path to a directory containing ADS scripts")
	enumFlags.StringVar(&args.Filepaths.TermOut, "o", "", "Path to the text file containing terminal stdout/stderr")
}

func runEnumCommand(clArgs []string) {
	// Seed the default pseudo-random number generator
	rand.Seed(time.Now().UTC().UnixNano())
	// Extract the correct config from the user provided arguments and/or configuration file
	cfg, args := argsAndConfig(clArgs)
	if cfg == nil {
		return
	}
	createOutputDirectory(cfg)

	rLog, wLog := io.Pipe()
	// Setup logging so that messages can be written to the file and used by the program
	cfg.Log = log.New(wLog, "", log.Lmicroseconds)
	logfile := filepath.Join(config.OutputDirectory(cfg.Dir), "amass.log")
	if args.Filepaths.LogFile != "" {
		logfile = args.Filepaths.LogFile
	}
	// Start handling the log messages
	go writeLogsAndMessages(rLog, logfile, args.Options.Verbose)
	// Create the System that will provide architecture to this enumeration
	sys, err := systems.NewLocalSystem(cfg)
	if err != nil {
		r.Fprintf(color.Error, "%v\n", err)
		os.Exit(1)
	}
	defer func() { _ = sys.Shutdown() }()

	if err := sys.SetDataSources(datasrcs.GetAllSources(sys)); err != nil {
		r.Fprintf(color.Error, "%v\n", err)
		os.Exit(1)
	}
	// Expand data source category names into the associated source names
	initializeSourceTags(sys.DataSources())
	cfg.SourceFilter.Sources = expandCategoryNames(cfg.SourceFilter.Sources, generateCategoryMap(sys))
	// Create the in-memory graph database used to store enumeration findings
	graph := netmap.NewGraph(netmap.NewCayleyGraphMemory())
	defer graph.Close()
	// Setup the new enumeration
	e := enum.NewEnumeration(cfg, sys, graph)
	if e == nil {
		r.Fprintf(color.Error, "%s\n", "Failed to setup the enumeration")
		os.Exit(1)
	}

	var wg sync.WaitGroup
	var outChans []chan *requests.Output
	// This channel sends the signal for goroutines to terminate
	done := make(chan struct{})
	// Print output only if JSONOutput is not meant for STDOUT
	if args.Filepaths.JSONOutput != "-" {
		wg.Add(1)
		// This goroutine will handle printing the output
		printOutChan := make(chan *requests.Output, 10)
		go printOutput(e, args, printOutChan, &wg)
		outChans = append(outChans, printOutChan)
	}

	wg.Add(1)
	// This goroutine will handle saving the output to the text file
	txtOutChan := make(chan *requests.Output, 10)
	go saveTextOutput(e, args, txtOutChan, &wg)
	outChans = append(outChans, txtOutChan)

	wg.Add(1)
	// This goroutine will handle saving the output to the JSON file
	jsonOutChan := make(chan *requests.Output, 10)
	go saveJSONOutput(e, args, jsonOutChan, &wg)
	outChans = append(outChans, jsonOutChan)

	var ctx context.Context
	var cancel context.CancelFunc
	if args.Timeout == 0 {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(args.Timeout)*time.Minute)
	}
	defer cancel()

	wg.Add(1)
	go processOutput(ctx, graph, e, outChans, done, &wg)
	// Monitor for cancellation by the user
	go func(d chan struct{}, c context.Context, f context.CancelFunc) {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(quit)

		select {
		case <-quit:
			f()
		case <-d:
		case <-c.Done():
		}
	}(done, ctx, cancel)
	// Start the enumeration process
	if err := e.Start(ctx); err != nil {
		r.Println(err)
		os.Exit(1)
	}
	// Let all the output goroutines know that the enumeration has finished
	close(done)
	wg.Wait()
	// If necessary, handle graph database migration
	if len(e.Sys.GraphDatabases()) > 0 {
		fmt.Fprintf(color.Error, "\n%s\n", green("The enumeration has finished"))

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		// Monitor for cancellation by the user
		go func(c context.CancelFunc) {
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(quit)

			<-quit
			c()
		}(cancel)
		// Copy the graph of findings into the system graph databases
		for _, g := range e.Sys.GraphDatabases() {
			fmt.Fprintf(color.Error, "%s%s%s\n",
				yellow("Discoveries are being migrated into the "), yellow(g.String()), yellow(" database"))

			if err := graph.Migrate(ctx, g); err != nil {
				fmt.Fprintf(color.Error, "%s%s%s%s\n",
					red("The database migration to "), red(g.String()), red(" failed: "), red(err.Error()))
			}
		}
	}
}

func argsAndConfig(clArgs []string) (*config.Config, *enumArgs) {
	args := enumArgs{
		AltWordList:       stringset.New(),
		AltWordListMask:   stringset.New(),
		BruteWordList:     stringset.New(),
		BruteWordListMask: stringset.New(),
		Blacklist:         stringset.New(),
		Domains:           stringset.New(),
		Excluded:          stringset.New(),
		Included:          stringset.New(),
		Names:             stringset.New(),
		Resolvers:         stringset.New(),
		Trusted:           stringset.New(),
	}
	var help1, help2 bool
	enumCommand := flag.NewFlagSet("enum", flag.ContinueOnError)

	enumBuf := new(bytes.Buffer)
	enumCommand.SetOutput(enumBuf)

	enumCommand.BoolVar(&help1, "h", false, "Show the program usage message")
	enumCommand.BoolVar(&help2, "help", false, "Show the program usage message")
	defineEnumArgumentFlags(enumCommand, &args)
	defineEnumOptionFlags(enumCommand, &args)
	defineEnumFilepathFlags(enumCommand, &args)

	if len(clArgs) < 1 {
		commandUsage(enumUsageMsg, enumCommand, enumBuf)
		return nil, &args
	}
	if err := enumCommand.Parse(clArgs); err != nil {
		r.Fprintf(color.Error, "%v\n", err)
		os.Exit(1)
	}
	if help1 || help2 {
		commandUsage(enumUsageMsg, enumCommand, enumBuf)
		return nil, &args
	}

	if args.Interface != "" {
		iface, err := net.InterfaceByName(args.Interface)
		if err != nil || iface == nil {
			fmt.Fprint(color.Output, format.InterfaceInfo())
			os.Exit(1)
		}
		if err := assignNetInterface(iface); err != nil {
			r.Fprintf(color.Error, "%v\n", err)
			os.Exit(1)
		}
	}
	if args.Options.NoColor {
		color.NoColor = true
	}
	if args.Options.Silent {
		color.Output = ioutil.Discard
		color.Error = ioutil.Discard
	}
	if args.AltWordListMask.Len() > 0 {
		args.AltWordList.Union(args.AltWordListMask)
	}
	if args.BruteWordListMask.Len() > 0 {
		args.BruteWordList.Union(args.BruteWordListMask)
	}
	if (args.Excluded.Len() > 0 || args.Filepaths.ExcludedSrcs != "") &&
		(args.Included.Len() > 0 || args.Filepaths.IncludedSrcs != "") {
		r.Fprintln(color.Error, "Cannot provide both include and exclude arguments")
		commandUsage(enumUsageMsg, enumCommand, enumBuf)
		os.Exit(1)
	}
	if err := processEnumInputFiles(&args); err != nil {
		fmt.Fprintf(color.Error, "%v\n", err)
		os.Exit(1)
	}

	cfg := config.NewConfig()
	// Check if a configuration file was provided, and if so, load the settings
	if err := config.AcquireConfig(args.Filepaths.Directory, args.Filepaths.ConfigFile, cfg); err == nil {
		// Check if a config file was provided that has DNS resolvers specified
		if len(cfg.Resolvers) > 0 && args.Resolvers.Len() == 0 {
			args.Resolvers = stringset.New(cfg.Resolvers...)
		}
	} else if args.Filepaths.ConfigFile != "" {
		r.Fprintf(color.Error, "Failed to load the configuration file: %v\n", err)
		os.Exit(1)
	}
	// Override configuration file settings with command-line arguments
	if err := cfg.UpdateConfig(args); err != nil {
		r.Fprintf(color.Error, "Configuration error: %v\n", err)
		os.Exit(1)
	}
	// Check if the user has requested the data source names
	if args.Options.ListSources {
		for _, line := range GetAllSourceInfo(cfg) {
			fmt.Fprintln(color.Output, line)
		}
		return nil, &args
	}
	// Some input validation
	if cfg.Passive && (args.Options.IPs || args.Options.IPv4 || args.Options.IPv6) {
		r.Fprintln(color.Error, "IP addresses cannot be provided without DNS resolution")
		os.Exit(1)
	}
	if !cfg.Active && len(args.Ports) > 0 {
		r.Fprintln(color.Error, "Ports can only be scanned in the active mode")
		os.Exit(1)
	}
	if len(cfg.Domains()) == 0 {
		r.Fprintln(color.Error, "Configuration error: No root domain names were provided")
		os.Exit(1)
	}
	return cfg, &args
}

func printOutput(e *enum.Enumeration, args *enumArgs, output chan *requests.Output, wg *sync.WaitGroup) {
	defer wg.Done()

	var total int
	tags := make(map[string]int)
	asns := make(map[int]*format.ASNSummaryData)
	// Print all the output returned by the enumeration
	for out := range output {
		out.Addresses = format.DesiredAddrTypes(out.Addresses, args.Options.IPv4, args.Options.IPv6)
		if !e.Config.Passive && len(out.Addresses) <= 0 {
			continue
		}

		total++
		if !args.Options.Passive {
			format.UpdateSummaryData(out, tags, asns)
		}

		source, name, ips := format.OutputLineParts(out, args.Options.Sources,
			args.Options.IPs || args.Options.IPv4 || args.Options.IPv6, args.Options.DemoMode)
		if ips != "" {
			ips = " " + ips
		}

		fmt.Fprintf(color.Output, "%s%s%s\n", blue(source), green(name), yellow(ips))
	}

	if total == 0 {
		r.Println("No names were discovered")
	} else if !args.Options.Passive {
		format.PrintEnumerationSummary(total, tags, asns, args.Options.DemoMode)
	}
}

func saveTextOutput(e *enum.Enumeration, args *enumArgs, output chan *requests.Output, wg *sync.WaitGroup) {
	defer wg.Done()

	dir := config.OutputDirectory(e.Config.Dir)
	txtfile := filepath.Join(dir, "amass.txt")
	if args.Filepaths.TermOut != "" {
		txtfile = args.Filepaths.TermOut
	}
	if args.Filepaths.AllFilePrefix != "" {
		txtfile = args.Filepaths.AllFilePrefix + ".txt"
	}
	if txtfile == "" {
		return
	}

	outptr, err := os.OpenFile(txtfile, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		r.Fprintf(color.Error, "Failed to open the text output file: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = outptr.Sync()
		_ = outptr.Close()
	}()

	_ = outptr.Truncate(0)
	_, _ = outptr.Seek(0, 0)
	// Save all the output returned by the enumeration
	for out := range output {
		out.Addresses = format.DesiredAddrTypes(out.Addresses, args.Options.IPv4, args.Options.IPv6)
		if !e.Config.Passive && len(out.Addresses) <= 0 {
			continue
		}

		source, name, ips := format.OutputLineParts(out, args.Options.Sources,
			args.Options.IPs || args.Options.IPv4 || args.Options.IPv6, args.Options.DemoMode)
		if ips != "" {
			ips = " " + ips
		}
		// Write the line to the output file
		fmt.Fprintf(outptr, "%s%s%s\n", source, name, ips)
	}
}

func saveJSONOutput(e *enum.Enumeration, args *enumArgs, output chan *requests.Output, wg *sync.WaitGroup) {
	defer wg.Done()

	dir := config.OutputDirectory(e.Config.Dir)
	jsonfile := filepath.Join(dir, "amass.json")
	if args.Filepaths.JSONOutput != "" {
		jsonfile = args.Filepaths.JSONOutput
	}
	if args.Filepaths.AllFilePrefix != "" {
		jsonfile = args.Filepaths.AllFilePrefix + ".json"
	}
	if jsonfile == "" {
		return
	}

	var jsonptr *os.File
	var err error

	// Write to STDOUT and not a file if named "-"
	if args.Filepaths.JSONOutput == "-" {
		jsonptr = os.Stdout
	} else {
		jsonptr, err = os.OpenFile(jsonfile, os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			r.Fprintf(color.Error, "Failed to open the JSON output file: %v\n", err)
			os.Exit(1)
		}
	}

	defer func() {
		_ = jsonptr.Sync()
		_ = jsonptr.Close()
	}()

	_ = jsonptr.Truncate(0)
	_, _ = jsonptr.Seek(0, 0)

	enc := json.NewEncoder(jsonptr)
	// Save all the output returned by the enumeration
	for out := range output {
		// Handle encoding the result as JSON
		_ = enc.Encode(out)
	}
}

func processOutput(ctx context.Context, g *netmap.Graph, e *enum.Enumeration, outputs []chan *requests.Output, done chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		// Signal all the other output goroutines to terminate
		for _, ch := range outputs {
			close(ch)
		}
	}()

	// This filter ensures that we only get new names
	known := stringset.New()
	defer known.Close()
	// The function that obtains output from the enum and puts it on the channel
	extract := func(limit int) {
		for _, o := range ExtractOutput(ctx, g, e, known, true, limit) {
			if !o.Complete(e.Config.Passive) || !e.Config.IsDomainInScope(o.Name) {
				continue
			}
			for _, ch := range outputs {
				ch <- o
			}
		}
	}

	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			extract(0)
			return
		case <-done:
			extract(0)
			return
		case <-t.C:
			extract(100)
		}
	}
}

func writeLogsAndMessages(logs *io.PipeReader, logfile string, verbose bool) {
	wildcard := regexp.MustCompile("DNS wildcard")
	queries := regexp.MustCompile("Querying")

	var filePtr *os.File
	if logfile != "" {
		var err error

		filePtr, err = os.OpenFile(logfile, os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			r.Fprintf(color.Error, "Failed to open the log file: %v\n", err)
		} else {
			defer func() {
				_ = filePtr.Sync()
				_ = filePtr.Close()
			}()
			_ = filePtr.Truncate(0)
			_, _ = filePtr.Seek(0, 0)
		}
	}

	scanner := bufio.NewScanner(logs)
	for scanner.Scan() {
		line := scanner.Text()
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(color.Error, "Error reading the Amass logs: %v\n", err)
			break
		}

		if filePtr != nil {
			fmt.Fprintln(filePtr, line)
		}
		// Remove the timestamp
		parts := strings.Split(line, " ")
		line = strings.Join(parts[1:], " ")
		// Check for Amass DNS wildcard messages
		if verbose && wildcard.FindString(line) != "" {
			fgR.Fprintln(color.Error, line)
		}
		// Let the user know when data sources are being queried
		if verbose && queries.FindString(line) != "" {
			fgY.Fprintln(color.Error, line)
		}
	}
}

// Obtain parameters from provided input files
func processEnumInputFiles(args *enumArgs) error {
	if args.Options.BruteForcing && len(args.Filepaths.BruteWordlist) > 0 {
		for _, f := range args.Filepaths.BruteWordlist {
			list, err := config.GetListFromFile(f)
			if err != nil {
				return fmt.Errorf("failed to parse the brute force wordlist file: %v", err)
			}
			args.BruteWordList.InsertMany(list...)
		}
	}
	if !args.Options.NoAlts && len(args.Filepaths.AltWordlist) > 0 {
		for _, f := range args.Filepaths.AltWordlist {
			list, err := config.GetListFromFile(f)
			if err != nil {
				return fmt.Errorf("failed to parse the alterations wordlist file: %v", err)
			}
			args.AltWordList.InsertMany(list...)
		}
	}
	if args.Filepaths.Blacklist != "" {
		list, err := config.GetListFromFile(args.Filepaths.Blacklist)
		if err != nil {
			return fmt.Errorf("failed to parse the blacklist file: %v", err)
		}
		args.Blacklist.InsertMany(list...)
	}
	if args.Filepaths.ExcludedSrcs != "" {
		list, err := config.GetListFromFile(args.Filepaths.ExcludedSrcs)
		if err != nil {
			return fmt.Errorf("failed to parse the exclude file: %v", err)
		}
		args.Excluded.InsertMany(list...)
	}
	if args.Filepaths.IncludedSrcs != "" {
		list, err := config.GetListFromFile(args.Filepaths.IncludedSrcs)
		if err != nil {
			return fmt.Errorf("failed to parse the include file: %v", err)
		}
		args.Included.InsertMany(list...)
	}
	if len(args.Filepaths.Names) > 0 {
		for _, f := range args.Filepaths.Names {
			list, err := config.GetListFromFile(f)
			if err != nil {
				return fmt.Errorf("failed to parse the subdomain names file: %v", err)
			}
			args.Names.InsertMany(list...)
		}
	}
	if len(args.Filepaths.Domains) > 0 {
		for _, f := range args.Filepaths.Domains {
			list, err := config.GetListFromFile(f)
			if err != nil {
				return fmt.Errorf("failed to parse the domain names file: %v", err)
			}
			lst1 := []string{}
			for _, x := range list {
				a1 := util.Convert2Domains(x)
				if 0 < len(a1) {
					lst1 = append(lst1, a1...)
				}
			}
			args.Domains.InsertMany(lst1...)
		}
	}
	if len(args.Filepaths.Resolvers) > 0 {
		for _, f := range args.Filepaths.Resolvers {
			list, err := config.GetListFromFile(f)
			if err != nil {
				return fmt.Errorf("failed to parse the esolver file: %v", err)
			}
			args.Resolvers.InsertMany(list...)
		}
	}
	return nil
}

// Setup the amass enumeration settings
func (e enumArgs) OverrideConfig(conf *config.Config) error {
	if len(e.Addresses) > 0 {
		conf.Addresses = e.Addresses
	}
	if len(e.ASNs) > 0 {
		conf.ASNs = e.ASNs
	}
	if len(e.CIDRs) > 0 {
		conf.CIDRs = e.CIDRs
	}
	if len(e.Ports) > 0 {
		conf.Ports = e.Ports
	}
	if e.Filepaths.Directory != "" {
		conf.Dir = e.Filepaths.Directory
	}
	if e.Filepaths.ScriptsDirectory != "" {
		conf.ScriptsDirectory = e.Filepaths.ScriptsDirectory
	}
	if e.Names.Len() > 0 {
		conf.ProvidedNames = e.Names.Slice()
	}
	if e.BruteWordList.Len() > 0 {
		conf.Wordlist = e.BruteWordList.Slice()
	}
	if e.AltWordList.Len() > 0 {
		conf.AltWordlist = e.AltWordList.Slice()
	}
	if e.Options.BruteForcing {
		conf.BruteForcing = true
	}
	if e.Options.Alterations {
		conf.Alterations = true
	}
	if e.Options.NoRecursive {
		conf.Recursive = false
	}
	if e.MinForRecursive != 1 {
		conf.MinForRecursive = e.MinForRecursive
	}
	if e.MaxDepth != 0 {
		conf.MaxDepth = e.MaxDepth
	}
	if e.Options.Active {
		conf.Active = true
		conf.Passive = false
	}
	if e.Options.Passive {
		conf.Passive = true
		conf.Active = false
		conf.BruteForcing = false
		conf.Alterations = false
	}
	if e.Blacklist.Len() > 0 {
		conf.Blacklist = e.Blacklist.Slice()
	}
	if e.Options.Verbose {
		conf.Verbose = true
	}
	if e.ResolverQPS > 0 {
		conf.ResolversQPS = e.ResolverQPS
	}
	if e.TrustedQPS > 0 {
		conf.TrustedQPS = e.TrustedQPS
	}
	if e.Resolvers.Len() > 0 {
		conf.SetResolvers(e.Resolvers.Slice()...)
	}
	if e.Trusted.Len() > 0 {
		conf.SetTrustedResolvers(e.Trusted.Slice()...)
	}
	if e.MaxDNSQueries > 0 {
		conf.MaxDNSQueries = e.MaxDNSQueries
	}
	if e.Included.Len() > 0 {
		conf.SourceFilter.Include = true
		// Check if brute forcing and alterations should be added
		if conf.Alterations {
			e.Included.Insert(requests.ALT)
		}
		if conf.BruteForcing {
			e.Included.Insert(requests.BRUTE)
		}
		conf.SourceFilter.Sources = e.Included.Slice()
	} else if e.Excluded.Len() > 0 || conf.Alterations || conf.BruteForcing {
		conf.SourceFilter.Include = false
		// Check if brute forcing and alterations should be added
		if conf.Alterations {
			e.Included.Insert(requests.ALT)
		}
		if conf.BruteForcing {
			e.Included.Insert(requests.BRUTE)
		}
		conf.SourceFilter.Sources = e.Excluded.Slice()
	}
	// Attempt to add the provided domains to the configuration
	conf.AddDomains(e.Domains.Slice()...)
	return nil
}
