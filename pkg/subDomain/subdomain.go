package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/OWASP/Amass/v3/config"
	"github.com/OWASP/Amass/v3/datasrcs"
	"github.com/OWASP/Amass/v3/format"
	amassnet "github.com/OWASP/Amass/v3/net"
	"github.com/OWASP/Amass/v3/requests"
	"github.com/OWASP/Amass/v3/resources"
	"github.com/OWASP/Amass/v3/systems"
	"github.com/caffix/netmap"
	"github.com/caffix/service"
	"github.com/caffix/stringset"
	"github.com/fatih/color"
	"net"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	mainUsageMsg         = "intel|enum|viz|track|db [options]"
	exampleConfigFileURL = "https://github.com/OWASP/Amass/blob/master/examples/config.ini"
	userGuideURL         = "https://github.com/OWASP/Amass/blob/master/doc/user_guide.md"
	tutorialURL          = "https://github.com/OWASP/Amass/blob/master/doc/tutorial.md"
)

var (
	// Colors used to ease the reading of program output
	g      = color.New(color.FgHiGreen)
	r      = color.New(color.FgHiRed)
	b      = color.New(color.FgHiBlue)
	fgR    = color.New(color.FgRed)
	fgY    = color.New(color.FgYellow)
	red    = color.New(color.FgHiRed).SprintFunc()
	yellow = color.New(color.FgHiYellow).SprintFunc()
	green  = color.New(color.FgHiGreen).SprintFunc()
	blue   = color.New(color.FgHiBlue).SprintFunc()
)

func commandUsage(msg string, cmdFlagSet *flag.FlagSet, errBuf *bytes.Buffer) {
	format.PrintBanner()
	g.Fprintf(color.Error, "Usage: %s %s\n\n", path.Base(os.Args[0]), msg)
	cmdFlagSet.PrintDefaults()
	g.Fprintln(color.Error, errBuf.String())

	if msg == mainUsageMsg {
		g.Fprintf(color.Error, "\nSubcommands: \n\n")
		g.Fprintf(color.Error, "\t%-11s - Discover targets for enumerations\n", "amass intel")
		g.Fprintf(color.Error, "\t%-11s - Perform enumerations and network mapping\n", "amass enum")
		g.Fprintf(color.Error, "\t%-11s - Visualize enumeration results\n", "amass viz")
		g.Fprintf(color.Error, "\t%-11s - Track differences between enumerations\n", "amass track")
		g.Fprintf(color.Error, "\t%-11s - Manipulate the Amass graph database\n", "amass db")
	}

	g.Fprintln(color.Error)
	g.Fprintf(color.Error, "The user's guide can be found here: \n%s\n\n", userGuideURL)
	g.Fprintf(color.Error, "An example configuration file can be found here: \n%s\n\n", exampleConfigFileURL)
	g.Fprintf(color.Error, "The Amass tutorial can be found here: \n%s\n\n", tutorialURL)
}

// 7.2k https://github.com/OWASP/Amass
// 5.9k https://github.com/OJ/gobuster
// 5.6k https://github.com/projectdiscovery/subfinder
func main() {
	var version, help1, help2 bool
	mainFlagSet := flag.NewFlagSet("amass", flag.ContinueOnError)

	defaultBuf := new(bytes.Buffer)
	mainFlagSet.SetOutput(defaultBuf)

	mainFlagSet.BoolVar(&help1, "h", false, "Show the program usage message")
	mainFlagSet.BoolVar(&help2, "help", false, "Show the program usage message")
	mainFlagSet.BoolVar(&version, "version", false, "Print the version number of this Amass binary")

	if len(os.Args) < 2 {
		commandUsage(mainUsageMsg, mainFlagSet, defaultBuf)
		return
	}
	if err := mainFlagSet.Parse(os.Args[1:]); err != nil {
		r.Fprintf(color.Error, "%v\n", err)
		os.Exit(1)
	}
	if help1 || help2 {
		commandUsage(mainUsageMsg, mainFlagSet, defaultBuf)
		return
	}
	if version {
		fmt.Fprintf(color.Error, "%s\n", format.Version)
		return
	}

	switch os.Args[1] {
	case "db":
		runDBCommand(os.Args[2:])
	case "enum":
		runEnumCommand(os.Args[2:])
	case "intel":
		runIntelCommand(os.Args[2:])
	case "track":
		runTrackCommand(os.Args[2:])
	case "viz":
		runVizCommand(os.Args[2:])
	case "help":
		runHelpCommand(os.Args[2:])
	default:
		commandUsage(mainUsageMsg, mainFlagSet, defaultBuf)
		os.Exit(1)
	}
}

// GetAllSourceInfo returns the output for the 'list' flag.
func GetAllSourceInfo(cfg *config.Config) []string {
	if cfg == nil {
		cfg = config.NewConfig()
	}

	sys, err := systems.NewLocalSystem(cfg)
	if err != nil {
		return []string{}
	}
	defer func() { _ = sys.Shutdown() }()

	srcs := datasrcs.SelectedDataSources(cfg, datasrcs.GetAllSources(sys))
	if err := sys.SetDataSources(srcs); err != nil {
		return []string{}
	}
	return DataSourceInfo(srcs, sys)
}

// DataSourceInfo acquires the information for data sources used by the provided System.
func DataSourceInfo(all []service.Service, sys systems.System) []string {
	var names []string

	names = append(names, fmt.Sprintf("%-35s%-35s%s", blue("Data Source"), blue("| Type"), blue("| Available")))
	var line string
	for i := 0; i < 8; i++ {
		line += blue("----------")
	}
	names = append(names, line)

	available := sys.DataSources()
	for _, src := range all {
		var avail string

		for _, a := range available {
			if src.String() == a.String() {
				avail = "*"
				break
			}
		}

		names = append(names, fmt.Sprintf("%-35s  %-35s  %s",
			green(src.String()), yellow(src.Description()), yellow(avail)))
	}

	return names
}

func createOutputDirectory(cfg *config.Config) {
	// Prepare output file paths
	dir := config.OutputDirectory(cfg.Dir)
	if dir == "" {
		r.Fprintln(color.Error, "Failed to obtain the output directory")
		os.Exit(1)
	}
	// If the directory does not yet exist, create it
	if err := os.MkdirAll(dir, 0755); err != nil {
		r.Fprintf(color.Error, "Failed to create the directory: %v\n", err)
		os.Exit(1)
	}
}

func generateCategoryMap(sys systems.System) map[string][]string {
	catToSources := make(map[string][]string)

	for _, src := range sys.DataSources() {
		t := src.Description()

		catToSources[t] = append(catToSources[t], src.String())
	}

	return catToSources
}

func expandCategoryNames(names []string, categories map[string][]string) []string {
	var newnames []string

	for _, name := range names {
		if _, found := categories[name]; found {
			newnames = append(newnames, categories[name]...)
			continue
		}

		newnames = append(newnames, name)
	}

	return newnames
}

func openGraphDatabase(dir string, cfg *config.Config) *netmap.Graph {
	for _, db := range cfg.GraphDBs {
		if !db.Primary {
			continue
		}

		cayley := netmap.NewCayleyGraph(db.System, db.URL, db.Options)
		if cayley == nil {
			return nil
		}

		g := netmap.NewGraph(cayley)
		if g == nil {
			return nil
		}

		return g
	}

	if db := cfg.LocalDatabaseSettings(cfg.GraphDBs); db != nil {
		db.Options = ""

		cayley := netmap.NewCayleyGraph(db.System, config.OutputDirectory(dir), db.Options)
		if cayley == nil {
			return nil
		}

		if g := netmap.NewGraph(cayley); g != nil {
			return g
		}
	}

	return nil
}

func memGraphForScope(ctx context.Context, domains []string, from *netmap.Graph) (*netmap.Graph, error) {
	db := netmap.NewGraph(netmap.NewCayleyGraphMemory())
	if db == nil {
		return nil, errors.New("failed to create the in-memory graph database")
	}

	var err error
	// Migrate the event data into the in-memory graph database
	if len(domains) == 0 {
		err = from.MigrateEvents(ctx, db)
	} else {
		err = from.MigrateEventsInScope(ctx, db, domains)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to move the data into the in-memory graph database: %v", err)
	}
	return db, nil
}

func orderedEvents(ctx context.Context, events []string, db *netmap.Graph) ([]string, []time.Time, []time.Time) {
	sort.Slice(events, func(i, j int) bool {
		var less bool

		e1, l1 := db.EventDateRange(ctx, events[i])
		e2, l2 := db.EventDateRange(ctx, events[j])
		if l2.After(l1) || e1.Before(e2) {
			less = true
		}

		return less
	})

	var earliest, latest []time.Time
	for _, event := range events {
		e, l := db.EventDateRange(ctx, event)

		earliest = append(earliest, e)
		latest = append(latest, l)
	}

	return events, earliest, latest
}

func getEventOutput(ctx context.Context, uuids []string, asninfo bool, db *netmap.Graph, cache *requests.ASNCache) []*requests.Output {
	filter := stringset.New()
	defer filter.Close()

	var output []*requests.Output
	for i := len(uuids) - 1; i >= 0; i-- {
		output = append(output, EventOutput(ctx, db, uuids[i], filter, asninfo, cache, 0)...)
	}
	return output
}

func domainNameInScope(name string, scope []string) bool {
	var discovered bool

	n := strings.ToLower(strings.TrimSpace(name))
	for _, d := range scope {
		d = strings.ToLower(d)

		if n == d || strings.HasSuffix(n, "."+d) {
			discovered = true
			break
		}
	}

	return discovered
}

func assignNetInterface(iface *net.Interface) error {
	addrs, err := iface.Addrs()
	if err != nil {
		return fmt.Errorf("network interface '%s' has no assigned addresses", iface.Name)
	}

	var best net.Addr
	for _, addr := range addrs {
		if a, ok := addr.(*net.IPNet); ok {
			if best == nil {
				best = a
			}
			if amassnet.IsIPv4(a.IP) {
				best = a
				break
			}
		}
	}

	if best == nil {
		return fmt.Errorf("network interface '%s' does not have assigned IP addresses", iface.Name)
	}

	amassnet.LocalAddr = best
	return nil
}

func cacheWithData() *requests.ASNCache {
	ranges, err := resources.GetIP2ASNData()
	if err != nil {
		return nil
	}

	cache := requests.NewASNCache()
	for _, r := range ranges {
		cache.Update(&requests.ASNRequest{
			Address:     r.FirstIP.String(),
			ASN:         r.ASN,
			CC:          r.CC,
			Prefix:      amassnet.Range2CIDR(r.FirstIP, r.LastIP).String(),
			Description: r.Description,
		})
	}
	return cache
}
