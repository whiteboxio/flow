package links

import (
	"github.com/whiteboxio/flow/pkg/core"
)

type DMX struct {
	Name string
	*core.Connector
}

func NewDMX(name string, _ core.Params) (core.Link, error) {
	dmx := &DMX{
		name,
		core.NewConnector(),
	}
	return dmx, nil
}

func (dmx *DMX) LinkTo(links []core.Link) error {
	for _, link := range links {
		if err := link.ConnectTo(dmx); err != nil {
			return err
		}
	}
	return nil
}
