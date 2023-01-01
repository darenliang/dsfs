package main

import (
	"flag"
	"github.com/bwmarrin/discordgo"
	"github.com/darenliang/dsfs/fuse"
	"go.uber.org/zap"
	"net/http"
	_ "net/http/pprof"
)

var (
	token         string
	userToken     bool
	guildID       string
	mount         string
	compact       bool
	webhook       bool
	debug         bool
	options       fuseOpts
	logger        *zap.SugaredLogger
	requiredFlags = []string{"t", "s"}
	// We need to jankily expose the db and dsfs for messageCreate and writer for webhooksUpdate
	db     *DB
	writer Writer
	dsfs   *Dsfs
)

func main() {
	flag.StringVar(&token, "t", "", "Token")
	flag.BoolVar(&userToken, "u", false, "Token is a user token")
	flag.StringVar(&guildID, "s", "", "Guild ID")
	flag.StringVar(&mount, "m", "", "Mount point")
	flag.BoolVar(&compact, "c", false, "Compact transactions")
	flag.BoolVar(&webhook, "w", false, "Use experimental webhooks")
	flag.BoolVar(&debug, "d", false, "Enable pprof and print debug logs")
	flag.Var(&options, "o", "FUSE options")
	flag.Parse()

	zapLogger, _ := zap.NewDevelopment()
	if debug {
		logger = zapLogger.Sugar()
		go func() {
			logger.Info("pprof running on port 8000")
			_ = http.ListenAndServe(":8000", nil)
		}()
	} else {
		zapLogger = zapLogger.WithOptions(zap.IncreaseLevel(zap.InfoLevel))
		logger = zapLogger.Sugar()
	}
	defer zapLogger.Sync()

	seen := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	for _, requiredFlag := range requiredFlags {
		if !seen[requiredFlag] {
			logger.Errorf("missing required -%s argument", requiredFlag)
			return
		}
	}

	var tokenPrefix string
	if !userToken {
		tokenPrefix = "Bot "
	}

	dg, err := discordgo.New(tokenPrefix + token)
	if err != nil {
		logger.Error("error creating Discord session,", err)
		return
	}

	dg.AddHandler(messageCreate)
	dg.AddHandler(webhooksUpdate)
	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentGuildWebhooks

	err = dg.Open()
	if err != nil {
		logger.Error("error opening connection,", err)
		return
	}

	// TODO: there are side effects that are preferably avoided
	db, err = setupDB(dg, guildID)
	if err != nil {
		logger.Error(err)
		return
	}

	writer, err = setupWriter(dg, webhook)
	if err != nil {
		logger.Error(err)
		return
	}

	dsfs = NewDsfs(dg, db, writer)
	host := fuse.NewFileSystemHost(dsfs)
	host.SetCapReaddirPlus(true)
	host.Mount(mount, options.Args())
}
