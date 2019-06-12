package pipeline

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"plugin"
	"runtime"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/awesome-flow/flow/pkg/cfg"
	"github.com/awesome-flow/flow/pkg/core"
	"github.com/awesome-flow/flow/pkg/global"
	"github.com/awesome-flow/flow/pkg/types"
	"github.com/awesome-flow/flow/pkg/util/data"

	evio_rcv "github.com/awesome-flow/flow/pkg/receiver/evio"
	http_rcv "github.com/awesome-flow/flow/pkg/receiver/http"
	tcp_rcv "github.com/awesome-flow/flow/pkg/receiver/tcp"
	udp_rcv "github.com/awesome-flow/flow/pkg/receiver/udp"
	unix_rcv "github.com/awesome-flow/flow/pkg/receiver/unix"

	buffer "github.com/awesome-flow/flow/pkg/link/buffer"
	compressor "github.com/awesome-flow/flow/pkg/link/compressor"
	demux "github.com/awesome-flow/flow/pkg/link/demux"
	fanout "github.com/awesome-flow/flow/pkg/link/fanout"
	meta_parser "github.com/awesome-flow/flow/pkg/link/meta_parser"
	mux "github.com/awesome-flow/flow/pkg/link/mux"
	replicator "github.com/awesome-flow/flow/pkg/link/replicator"
	router "github.com/awesome-flow/flow/pkg/link/router"
	throttler "github.com/awesome-flow/flow/pkg/link/throttler"

	dumper_sink "github.com/awesome-flow/flow/pkg/sink/dumper"
	null_sink "github.com/awesome-flow/flow/pkg/sink/null"
	tcp_sink "github.com/awesome-flow/flow/pkg/sink/tcp"
	udp_sink "github.com/awesome-flow/flow/pkg/sink/udp"
)

type Pipeline struct {
	pplCfg   map[string]types.CfgBlockPipeline
	compsCfg map[string]types.CfgBlockActor
	compTop  *data.Topology
}

type Constructor func(string, types.Params, *core.Context) (core.Link, error)

var (
	CompBuilders = map[string]Constructor{
		"receiver.tcp":  tcp_rcv.New,
		"receiver.udp":  udp_rcv.New,
		"receiver.http": http_rcv.New,
		"receiver.unix": unix_rcv.New,
		"receiver.evio": evio_rcv.New,

		"link.demux":       demux.New,
		"link.mux":         mux.New,
		"link.router":      router.New,
		"link.throttler":   throttler.New,
		"link.fanout":      fanout.New,
		"link.replicator":  replicator.New,
		"link.buffer":      buffer.New,
		"link.meta_parser": meta_parser.New,
		"link.compressor":  compressor.New,

		"sink.dumper": dumper_sink.New,
		"sink.tcp":    tcp_sink.New,
		"sink.udp":    udp_sink.New,
		"sink.null":   null_sink.New,
	}
)

func buildComponents(cfg map[string]types.CfgBlockActor) (map[string]core.Link, error) {
	components := make(map[string]core.Link)
	for name, params := range cfg {
		ctx := core.NewContext()
		if _, ok := components[name]; ok {
			return nil, fmt.Errorf("Duplicate declaration of component %q", name)
		}
		comp, err := buildComponent(name, params, ctx)
		if err != nil {
			return nil, err
		}

		components[name] = comp
	}

	return components, nil
}

func NewPipeline(
	compsCfg map[string]types.CfgBlockActor,
	pplCfg map[string]types.CfgBlockPipeline) (*Pipeline, error) {

	components, err := buildComponents(compsCfg)
	if err != nil {
		return nil, err
	}

	for compName, compCfg := range pplCfg {
		comp, ok := components[compName]
		if !ok {
			return nil, fmt.Errorf(
				"Pipeline component %q mentioned in the pipeline config but never defined in components section", compName)
		}
		if len(compCfg.Connect) != 0 {
			for _, connect := range compCfg.Connect {
				log.Infof("Connecting %s to %s", compName, connect)
				if _, ok := components[connect]; !ok {
					return nil, fmt.Errorf(
						"Failed to connect %s to %s: %s is undefined",
						compName, compCfg.Connect, connect)
				}
				if err := comp.ConnectTo(components[connect]); err != nil {
					return nil, fmt.Errorf("Failed to connect %s to %s: %s",
						compName, connect, err.Error())
				}
			}
		}
	}

	topology, err := buildPipelineTopology(pplCfg, components)
	if err != nil {
		return nil, err
	}

	pipeline := &Pipeline{
		pplCfg:   pplCfg,
		compsCfg: compsCfg,
		compTop:  topology,
	}

	pipeline.applySysCfg()

	return pipeline, nil
}

func componentIsPlugin(cfg types.CfgBlockActor) bool {
	return len(cfg.Plugin) > 0
}

func buildComponent(compName string, cfg types.CfgBlockActor, context *core.Context) (core.Link, error) {
	if componentIsPlugin(cfg) {
		return buildPlugin(compName, cfg, context)
	}
	if builder, ok := CompBuilders[cfg.Module]; ok {
		return builder(compName, types.Params(cfg.Params), context)
	}
	return nil, fmt.Errorf("Unknown module: %s requested by %s", cfg.Module, compName)
}

// TODO: refactoring
func buildPlugin(name string, compcfg types.CfgBlockActor, context *core.Context) (core.Link, error) {
	if compcfg.Plugin == "" {
		return nil, fmt.Errorf("%q config does not look like a plugin", name)
	}

	repo, ok := global.Load("config")
	if !ok {
		return nil, fmt.Errorf("Failed to load config repo from global storage")
	}

	pathval, ok := repo.(*cfg.Repository).Get(types.NewKey("plugin.path"))
	if !ok {
		return nil, fmt.Errorf("Failed to get plugin.path from config repo")
	}
	path := pathval.(string)

	// /plugin_base/path/plugin_name/plugin_name.so
	fullpath := filepath.Join(path, compcfg.Plugin, fmt.Sprintf("%s.so", compcfg.Plugin))
	log.Debugf("Initializing plugin %q from path: %s", compcfg.Plugin, fullpath)

	if _, err := os.Stat(fullpath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Unable to find plugin shared library object under path: %s", fullpath)
	}
	pl, err := plugin.Open(fullpath)
	if err != nil {
		return nil, err
	}
	log.Debugf("Successfully red plugin %q shared library object. Looking up for constructor function %q", compcfg.Plugin, compcfg.Constructor)

	cnstr, err := pl.Lookup(compcfg.Constructor)
	if err != nil {
		return nil, fmt.Errorf("Failed to find the declared constructor function %q for plugin %s: %s", compcfg.Constructor, compcfg.Plugin, err)
	}

	lnk, err := cnstr.(func(string, types.Params, *core.Context) (core.Link, error))(name, types.Params(compcfg.Params), context)
	if err != nil {
		return nil, err
	}

	if lnk == nil {
		return nil, fmt.Errorf("Plugin %s constructor %s returned a nil object an no error", compcfg.Plugin, compcfg.Constructor)
	}

	return lnk, nil
}

func (ppl *Pipeline) Explain() (string, error) {
	dotexplain := &DotExplainer{}
	return dotexplain.Explain(ppl)
}

func (ppl *Pipeline) Links() []core.Link {
	sorted, err := ppl.compTop.Sort()
	if err != nil {
		panic(err.Error())
	}
	// Reverse the list
	for i := 0; i < len(sorted)/2; i++ {
		sorted[i], sorted[len(sorted)-1-i] = sorted[len(sorted)-1-i], sorted[i]
	}

	links := make([]core.Link, 0, len(sorted))
	for _, node := range sorted {
		links = append(links, node.(core.Link))
	}

	return links
}

func (ppl *Pipeline) ExecCmd(cmd *core.Cmd, cmdPpgt core.CmdPropagation) error {
	sorted, err := ppl.compTop.Sort()
	if err != nil {
		return err
	}
	switch cmdPpgt {
	case core.CmdPpgtTopDwn:
		l := len(sorted)
		for i := 0; i < l/2; i++ {
			sorted[i], sorted[l-1-i] = sorted[l-1-i], sorted[i]
		}
	case core.CmdPpgtBtmUp:
	default:
		return fmt.Errorf("Unknown command propagation: %d", cmdPpgt)
	}

	for _, topNode := range sorted {
		if err := topNode.(core.Link).ExecCmd(cmd); err != nil {
			return err
		}
	}

	return nil
}

func (ppl *Pipeline) Start() error {
	rand.Seed(time.Now().UTC().UnixNano())
	return ppl.ExecCmd(&core.Cmd{Code: core.CmdCodeStart}, core.CmdPpgtBtmUp)
}

func (ppl *Pipeline) Stop() error {
	return ppl.ExecCmd(&core.Cmd{Code: core.CmdCodeStop}, core.CmdPpgtTopDwn)
}

func (ppl *Pipeline) applySysCfg() error {
	repo, ok := global.Load("config")
	if !ok {
		return fmt.Errorf("Failed to load config repo from global store")
	}
	maxproc, ok := repo.(*cfg.Repository).Get(types.NewKey("system.maxprocs"))
	if !ok {
		return fmt.Errorf("Failed to load system.maxproc config from config repo")
	}

	log.Infof("Setting GOMAXPROCS to %d", maxproc.(int))
	runtime.GOMAXPROCS(maxproc.(int))

	return nil
}

func buildPipelineTopology(cfg map[string]types.CfgBlockPipeline,
	components map[string]core.Link) (*data.Topology, error) {
	top := data.NewTopology()

	for _, component := range components {
		top.AddNode(component)
	}

	for name, blockcfg := range cfg {
		hasConnection := blockHasConnection(blockcfg)
		if hasConnection {
			for _, connect := range blockcfg.Connect {
				connectTo, ok := components[connect]
				if !ok {
					return nil, fmt.Errorf(
						"Component %q defined a connection to an unknown component %q",
						name,
						connect)
				}
				top.Connect(components[name], connectTo)
			}
		}
	}

	return top, nil
}

func blockHasConnection(blockcfg types.CfgBlockPipeline) bool {
	return len(blockcfg.Connect) > 0
}
