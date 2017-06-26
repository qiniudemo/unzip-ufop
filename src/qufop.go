package main

import (
	"fmt"
	"github.com/qiniu/log"
	"os"
	"runtime"
	"ufop"
	"ufop/unzip"
)

const (
	VERSION = "2.0"
)

func help() {
	fmt.Printf("Usage: qufop <UfopConfig>\r\n\r\nVERSION: %s\r\n", VERSION)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	log.SetOutput(os.Stdout)

	args := os.Args
	argc := len(args)

	var configFilePath string

	switch argc {
	case 2:
		configFilePath = args[1]
	default:
		help()
		return
	}

	//load config
	ufopConf := &ufop.UfopConfig{}
	confErr := ufopConf.LoadFromFile(configFilePath)
	if confErr != nil {
		log.Error("load config file error,", confErr)
		return
	}

	ufopServ := ufop.NewServer(ufopConf)

	if err := ufopServ.RegisterJobHandler("unzip.conf", &unzip.Unzipper{}); err != nil {
		log.Error(err)
	}

	//listen
	ufopServ.Listen()
}
