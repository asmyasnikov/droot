package commands

import (
	"github.com/urfave/cli"

	"github.com/asmyasnikov/droot/log"
)

var Commands = []cli.Command{
	CommandExport,
	CommandRun,
	CommandUmount,
}

func fatalOnError(command func(context *cli.Context) error) func(context *cli.Context) {
	return func(context *cli.Context) {
		if err := command(context); err != nil {
			log.Error(err)
		}
	}
}
